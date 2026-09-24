package adoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yet-an-other/page-hub/internal/config"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// PlannedObject is the exact observed state of one declared object.
type PlannedObject struct {
	Key             string            `json:"key"`
	RelativePath    string            `json:"relativePath"`
	Size            int64             `json:"size"`
	ETag            string            `json:"etag"`
	ModifiedAt      string            `json:"modifiedAt"`
	ContentType     string            `json:"contentType"`
	ContentEncoding string            `json:"contentEncoding"`
	CacheControl    string            `json:"cacheControl"`
	UserMetadata    map[string]string `json:"userMetadata"`
	SHA256          string            `json:"sha256"`
}

// PlannedProbe is one declared public-route probe together with the observed
// behavior that planning captured. Both are bound by the plan digest, so a
// changed route, status, or body invalidates the plan.
type PlannedProbe struct {
	Path           string `json:"path"`
	URL            string `json:"url"`
	ExpectStatus   int    `json:"expectStatus"`
	ObservedStatus int    `json:"observedStatus"`
	ContentType    string `json:"contentType"`
	BodySHA256     string `json:"bodySha256"`
}

// PlannedPublication is the exact proposed accepted state of one candidate.
type PlannedPublication struct {
	Project          ProjectDeclaration `json:"project"`
	Path             string             `json:"path"`
	DisplayName      string             `json:"displayName"`
	EntryPoint       string             `json:"entryPoint"`
	RoutingMode      RoutingMode        `json:"routingMode"`
	CanonicalURL     string             `json:"canonicalUrl"`
	ContentChangedAt string             `json:"contentChangedAt"`
	Objects          []PlannedObject    `json:"objects"`
	Probes           []PlannedProbe     `json:"probes"`
}

// PlanContent is everything a plan asserts. The digest is computed over its
// canonical JSON encoding, so any changed byte, metadata value, key set, or
// probe result invalidates the plan.
type PlanContent struct {
	Version       int                  `json:"version"`
	PublicBaseURL string               `json:"publicBaseURL"`
	Declaration   Declaration          `json:"declaration"`
	Publications  []PlannedPublication `json:"publications"`
}

// Plan is the reviewable planning result: the asserted content plus the
// digest binding it.
type Plan struct {
	Content PlanContent `json:"plan"`
	Digest  string      `json:"digest"`
}

// BuildPlan reads the declared objects through the production storage
// adapter, probes the declared public routes, and emits a reviewable plan.
// Planning changes neither storage nor the catalog and issues only reads.
func BuildPlan(ctx context.Context, reader storage.Reader, prober RouteProber, publicBaseURL string, declaration Declaration) (Plan, error) {
	if reader == nil {
		return Plan{}, fmt.Errorf("storage reader must not be nil")
	}
	if prober == nil {
		return Plan{}, fmt.Errorf("public-route prober must not be nil")
	}
	normalizedBase, err := normalizePublicBaseURL(publicBaseURL)
	if err != nil {
		return Plan{}, err
	}
	if err := declaration.Validate(); err != nil {
		return Plan{}, fmt.Errorf("invalid declaration: %w", err)
	}

	listing, err := reader.ListObjects(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list storage: %w", err)
	}
	observed := make(map[string]storage.ObjectListing, len(listing))
	for _, entry := range listing {
		observed[entry.Key] = entry
	}
	if err := checkNoUnexpectedObjects(observed, declaration); err != nil {
		return Plan{}, err
	}

	plan := PlanContent{Version: 1, PublicBaseURL: normalizedBase, Declaration: declaration}
	planning := planningContext{reader: reader, prober: prober, baseURL: normalizedBase, observed: observed}
	for _, publication := range declaration.Publications {
		planned, err := planPublication(ctx, planning, publication)
		if err != nil {
			return Plan{}, err
		}
		plan.Publications = append(plan.Publications, planned)
	}

	digest, err := Digest(plan)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Content: plan, Digest: digest}, nil
}

// planningContext carries the read-only seams one planning pass needs.
type planningContext struct {
	reader   storage.Reader
	prober   RouteProber
	baseURL  string
	observed map[string]storage.ObjectListing
}

func planPublication(ctx context.Context, planning planningContext, publication PublicationDeclaration) (PlannedPublication, error) {
	canonical := CanonicalURL(planning.baseURL, publication.Path)
	planned := PlannedPublication{
		Project:      publication.Project,
		Path:         publication.Path,
		DisplayName:  publication.DisplayNameOr(defaultDisplayName(publication)),
		EntryPoint:   publication.EntryPoint,
		RoutingMode:  publication.RoutingMode,
		CanonicalURL: canonical,
	}
	var newest time.Time
	for _, key := range publication.Objects {
		listed, ok := planning.observed[key]
		if !ok {
			return PlannedPublication{}, fmt.Errorf("declared object %q does not exist in storage", key)
		}
		content, err := planning.reader.ReadObject(ctx, key)
		if err != nil {
			return PlannedPublication{}, fmt.Errorf("read declared object %q: %w", key, err)
		}
		modified := content.LastModified
		if modified.IsZero() {
			modified = listed.LastModified
		}
		if modified.After(newest) {
			newest = modified
		}
		planned.Objects = append(planned.Objects, PlannedObject{
			Key:             key,
			RelativePath:    RelativePath(publication.Path, key),
			Size:            content.Size,
			ETag:            content.ETag,
			ModifiedAt:      modified.UTC().Format(time.RFC3339),
			ContentType:     content.ContentType,
			ContentEncoding: content.ContentEncoding,
			CacheControl:    content.CacheControl,
			UserMetadata:    metadataOrEmpty(content.UserMetadata),
			SHA256:          content.SHA256,
		})
	}
	sortObjects(planned.Objects)
	planned.ContentChangedAt = newest.UTC().Format(time.RFC3339)

	for _, probe := range publication.Probes {
		probeURL := ProbeURL(canonical, probe.Path)
		observation, err := planning.prober.Probe(ctx, probeURL)
		if err != nil {
			return PlannedPublication{}, fmt.Errorf("public route probe failed for %q: %w", publication.Path, err)
		}
		if observation.Status != probe.ExpectStatus {
			return PlannedPublication{}, fmt.Errorf(
				"public route probe %s returned %d, expected %d; adoption would not preserve the declared behavior",
				probeURL, observation.Status, probe.ExpectStatus)
		}
		planned.Probes = append(planned.Probes, PlannedProbe{
			Path:           probe.Path,
			URL:            probeURL,
			ExpectStatus:   probe.ExpectStatus,
			ObservedStatus: observation.Status,
			ContentType:    observation.ContentType,
			BodySHA256:     observation.BodySHA256,
		})
	}
	sortProbes(planned.Probes)
	return planned, nil
}

// checkNoUnexpectedObjects rejects storage objects that the declaration does
// not claim. During the adoption write freeze the batch covers the bucket;
// anything else is drift the operator must resolve before adopting.
func checkNoUnexpectedObjects(observed map[string]storage.ObjectListing, declaration Declaration) error {
	declared := map[string]bool{}
	for _, publication := range declaration.Publications {
		for _, key := range publication.Objects {
			declared[key] = true
		}
	}
	unexpected := make([]string, 0)
	for key := range observed {
		if !declared[key] {
			unexpected = append(unexpected, key)
		}
	}
	if len(unexpected) == 0 {
		return nil
	}
	sort.Strings(unexpected)
	shown := unexpected
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Errorf("storage holds %d unexpected object(s) outside the declaration, including %s; a changed layout stops planning",
		len(unexpected), strings.Join(quoteKeys(shown), ", "))
}

func quoteKeys(keys []string) []string {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = fmt.Sprintf("%q", key)
	}
	return quoted
}

// normalizePublicBaseURL validates the canonical public origin through the
// shared configuration rule so plan and commit agree on one definition.
func normalizePublicBaseURL(baseURL string) (string, error) {
	return config.NormalizePublicBaseURL(baseURL)
}

// Digest returns the canonical SHA-256 digest of plan content.
func Digest(content PlanContent) (string, error) {
	// json.Marshal is canonical for this shape: struct field order is fixed
	// and map keys are sorted, so identical content always produces the same
	// digest bytes.
	encoded, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("encode plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Verify checks that a plan's digest matches its content.
func (p Plan) Verify() error {
	digest, err := Digest(p.Content)
	if err != nil {
		return err
	}
	if digest != p.Digest {
		return fmt.Errorf("plan digest %s does not match its content (%s)", p.Digest, digest)
	}
	return nil
}

func sortObjects(objects []PlannedObject) {
	sort.SliceStable(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
}

func sortProbes(probes []PlannedProbe) {
	sort.SliceStable(probes, func(i, j int) bool { return probes[i].Path < probes[j].Path })
}

func metadataOrEmpty(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	return metadata
}

func defaultDisplayName(publication PublicationDeclaration) string {
	if publication.Path == publication.Project.Prefix {
		return publication.Project.Prefix
	}
	if index := strings.LastIndex(publication.Path, "/"); index >= 0 {
		return publication.Path[index+1:]
	}
	return publication.Path
}
