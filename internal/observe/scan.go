// Package observe compares accepted manifests with observed storage. A full
// observation lists the complete bucket, classifies every managed Publication
// as in sync, drifted, or missing, records unexpected objects as unclaimed
// storage, and derives exact usage and the storage-mutation lock the
// observation implies. Observations never change accepted state (ADR-0001)
// and never issue a storage write.
package observe

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// modifiedTimeTolerance absorbs RFC3339 second truncation: adoption records
// content-change times without sub-second precision.
const modifiedTimeTolerance = time.Second

// statusDetailBound keeps per-object findings readable in the manager.
const statusDetailBound = 5

// Options tunes one observation pass.
type Options struct {
	// Complete requests body downloads and SHA-256 verification of every
	// accepted object even when listing and metadata match, as a complete
	// reconciliation requires.
	Complete bool

	// Now stamps the observation. Tests inject a fixed clock.
	Now func() time.Time
}

// PublicationResult is the comparison result for one managed Publication.
type PublicationResult struct {
	PublicationID string
	Path          string
	State         string
	ObservedSize  *int64
	StatusDetail  string
}

// Result is one complete bucket observation, kept separate from accepted
// state. Usage.QuotaBytes stays zero here: quota is deployment configuration
// and the manager API attaches it.
type Result struct {
	ObservedAt    time.Time
	Publications  []PublicationResult
	UnclaimedKeys []string
	Usage         catalog.BucketUsage
	MutationLock  string
}

// Record converts the result into its durable catalog form.
func (r Result) Record() catalog.ObservationRecord {
	record := catalog.ObservationRecord{
		ObservedAt:     r.ObservedAt,
		MutationLock:   r.MutationLock,
		TotalBytes:     r.Usage.TotalBytes,
		AcceptedBytes:  r.Usage.AcceptedBytes,
		UnclaimedBytes: r.Usage.UnclaimedBytes,
		UnclaimedKeys:  r.UnclaimedKeys,
	}
	for _, publication := range r.Publications {
		record.Publications = append(record.Publications, catalog.PublicationObservationRecord{
			PublicationID: publication.PublicationID,
			State:         publication.State,
			ObservedSize:  publication.ObservedSize,
			StatusDetail:  publication.StatusDetail,
		})
	}
	return record
}

// Scan compares accepted manifests with the complete bucket listing. It
// downloads and hashes bodies only when the reconciliation contract requires
// it: when listing or metadata differs from the accepted manifest, or when a
// complete reconciliation requests it.
func Scan(ctx context.Context, reader storage.Reader, manifests []catalog.PublicationManifest, options Options) (Result, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}

	listing, err := reader.ListObjects(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list bucket: %w", err)
	}
	observed := make(map[string]storage.ObjectListing, len(listing))
	var totalBytes int64
	for _, entry := range listing {
		observed[entry.Key] = entry
		totalBytes += entry.Size
	}

	result := Result{ObservedAt: now().UTC()}
	claimed := make(map[string]bool)
	var acceptedBytes int64
	for _, manifest := range manifests {
		for _, object := range manifest.Objects {
			claimed[object.Key] = true
			acceptedBytes += object.Size
		}
	}
	for key := range observed {
		if !claimed[key] {
			result.UnclaimedKeys = append(result.UnclaimedKeys, key)
			result.Usage.UnclaimedBytes += observed[key].Size
		}
	}
	sort.Strings(result.UnclaimedKeys)

	for _, manifest := range manifests {
		publication, err := scanPublication(ctx, reader, manifest, observed, options)
		if err != nil {
			return Result{}, err
		}
		result.Publications = append(result.Publications, publication)
	}

	result.Usage.TotalBytes = totalBytes
	result.Usage.AcceptedBytes = acceptedBytes
	if len(result.UnclaimedKeys) > 0 || anyFinding(result.Publications) {
		result.MutationLock = catalog.LockGlobal
	} else {
		result.MutationLock = catalog.LockNone
	}
	return result, nil
}

func anyFinding(publications []PublicationResult) bool {
	for _, publication := range publications {
		if publication.State != catalog.StateInSync {
			return true
		}
	}
	return false
}

// objectFinding collects every difference found for one accepted object.
type objectFinding struct {
	key           string
	missing       bool
	diffs         []string
	bytesDiffer   bool
	bytesVerified bool
}

func scanPublication(ctx context.Context, reader storage.Reader, manifest catalog.PublicationManifest, observed map[string]storage.ObjectListing, options Options) (PublicationResult, error) {
	result := PublicationResult{
		PublicationID: manifest.PublicationID,
		Path:          manifest.Path,
		State:         catalog.StateInSync,
	}

	if len(manifest.Objects) == 0 {
		result.State = catalog.StateMissing
		result.StatusDetail = "the accepted manifest holds no objects"
		return result, nil
	}

	var findings []objectFinding
	var foundKeys int
	var observedSize int64
	for _, accepted := range manifest.Objects {
		finding, ok, err := compareObject(ctx, reader, accepted, observed, options)
		if err != nil {
			return PublicationResult{}, err
		}
		if ok {
			foundKeys++
			observedSize += observed[accepted.Key].Size
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
	}

	if foundKeys == 0 {
		result.State = catalog.StateMissing
		result.StatusDetail = fmt.Sprintf("all %d accepted object(s) are absent from storage", len(manifest.Objects))
		return result, nil
	}
	if foundKeys > 0 {
		size := observedSize
		result.ObservedSize = &size
	}
	if len(findings) > 0 {
		result.State = catalog.StateDrifted
		result.StatusDetail = describeFindings(findings, len(manifest.Objects))
	}
	return result, nil
}

// compareObject compares one accepted object with observed storage. It
// returns nil when the object matches its accepted manifest. The boolean
// reports whether the object is present in the listing.
func compareObject(ctx context.Context, reader storage.Reader, accepted catalog.ManifestObject, observed map[string]storage.ObjectListing, options Options) (*objectFinding, bool, error) {
	entry, listed := observed[accepted.Key]
	if !listed {
		return &objectFinding{key: accepted.Key, missing: true}, false, nil
	}
	finding := &objectFinding{key: accepted.Key}

	etagDiff := entry.ETag != accepted.ETag
	if entry.Size != accepted.Size {
		finding.diffs = append(finding.diffs, fmt.Sprintf("size changed (%d → %d B)", accepted.Size, entry.Size))
	}
	if etagDiff {
		finding.diffs = append(finding.diffs, "ETag changed")
	}

	meta, err := reader.StatObject(ctx, accepted.Key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			// The listing saw it, storage no longer has it: race treated as
			// absence so the Publication reports ordinary drift.
			return &objectFinding{key: accepted.Key, missing: true}, false, nil
		}
		return nil, false, fmt.Errorf("read metadata for %q: %w", accepted.Key, err)
	}
	acceptedModified, err := parseTime(accepted.ModifiedAt)
	if err != nil {
		return nil, false, fmt.Errorf("accepted modified time for %q: %w", accepted.Key, err)
	}
	if !withinTolerance(meta.LastModified, acceptedModified) {
		finding.diffs = append(finding.diffs, "modification time changed")
	}
	if meta.ETag != accepted.ETag && !etagDiff {
		finding.diffs = append(finding.diffs, "ETag changed")
	}
	if meta.ContentType != accepted.ContentType {
		finding.diffs = append(finding.diffs, fmt.Sprintf("content type changed (%s → %s)", accepted.ContentType, meta.ContentType))
	}
	if meta.ContentEncoding != accepted.ContentEncoding {
		finding.diffs = append(finding.diffs, fmt.Sprintf("content encoding changed (%s → %s)", accepted.ContentEncoding, meta.ContentEncoding))
	}
	if meta.CacheControl != accepted.CacheControl {
		finding.diffs = append(finding.diffs, fmt.Sprintf("cache control changed (%s → %s)", accepted.CacheControl, meta.CacheControl))
	}
	if !sameEntries(meta.UserMetadata, accepted.UserMetadata) {
		finding.diffs = append(finding.diffs, "user metadata changed")
	}

	// Ordinary unchanged observations never download bodies. A difference in
	// listing or metadata, or a complete reconciliation, verifies the bytes.
	if len(finding.diffs) == 0 && !options.Complete {
		return nil, true, nil
	}
	content, err := reader.ReadObject(ctx, accepted.Key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			return &objectFinding{key: accepted.Key, missing: true}, false, nil
		}
		return nil, false, fmt.Errorf("read object %q: %w", accepted.Key, err)
	}
	if content.SHA256 != accepted.SHA256 {
		finding.bytesVerified = true
		finding.bytesDiffer = true
		return finding, true, nil
	}
	if len(finding.diffs) > 0 {
		finding.bytesVerified = true
		return finding, true, nil
	}
	// Verified in a complete reconciliation and unchanged.
	return nil, true, nil
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %q: %w", value, err)
	}
	return parsed, nil
}

func withinTolerance(observed, accepted time.Time) bool {
	if observed.IsZero() || accepted.IsZero() {
		return observed.Equal(accepted)
	}
	difference := observed.Sub(accepted)
	if difference < 0 {
		difference = -difference
	}
	return difference <= modifiedTimeTolerance
}

func sameEntries(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		other, ok := right[name]
		if !ok || other != value {
			return false
		}
	}
	return true
}

func describeFindings(findings []objectFinding, totalObjects int) string {
	ordered := make([]objectFinding, len(findings))
	copy(ordered, findings)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].key < ordered[j].key })

	var parts []string
	shown := ordered
	if len(shown) > statusDetailBound {
		shown = shown[:statusDetailBound]
	}
	for _, finding := range shown {
		if finding.missing {
			parts = append(parts, finding.key+": absent from storage")
			continue
		}
		var detail strings.Builder
		detail.WriteString(finding.key)
		if len(finding.diffs) > 0 {
			detail.WriteString(": ")
			detail.WriteString(strings.Join(finding.diffs, ", "))
		}
		if finding.bytesVerified {
			if len(finding.diffs) > 0 {
				detail.WriteString(", ")
			} else {
				detail.WriteString(": ")
			}
			if finding.bytesDiffer {
				detail.WriteString("body verified different")
			} else {
				detail.WriteString("body verified unchanged")
			}
		}
		parts = append(parts, detail.String())
	}
	if remainder := len(ordered) - len(shown); remainder > 0 {
		parts = append(parts, fmt.Sprintf("%d more object(s) differ", remainder))
	}
	return strings.Join(parts, "; ")
}
