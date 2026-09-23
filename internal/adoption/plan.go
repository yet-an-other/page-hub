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

// PlannedPublication is the exact proposed accepted state of one candidate.
type PlannedPublication struct {
	Project          ProjectDeclaration `json:"project"`
	Path             string             `json:"path"`
	DisplayName      string             `json:"displayName"`
	EntryPoint       string             `json:"entryPoint"`
	RoutingMode      RoutingMode        `json:"routingMode"`
	ContentChangedAt string             `json:"contentChangedAt"`
	Objects          []PlannedObject    `json:"objects"`
}

// PlanContent is everything a plan asserts. The digest is computed over its
// canonical JSON encoding, so any changed byte, metadata value, or key set
// invalidates the plan.
type PlanContent struct {
	Version      int                  `json:"version"`
	Declaration  Declaration          `json:"declaration"`
	Publications []PlannedPublication `json:"publications"`
}

// Plan is the reviewable planning result: the asserted content plus the
// digest binding it.
type Plan struct {
	Content PlanContent `json:"plan"`
	Digest  string      `json:"digest"`
}

// BuildPlan reads the declared objects through the production storage adapter and
// emits a reviewable plan. Planning changes neither storage nor the catalog.
func BuildPlan(ctx context.Context, reader storage.Reader, declaration Declaration) (Plan, error) {
	if reader == nil {
		return Plan{}, fmt.Errorf("storage reader must not be nil")
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

	plan := PlanContent{Version: 1, Declaration: declaration}
	for _, publication := range declaration.Publications {
		planned := PlannedPublication{
			Project:     publication.Project,
			Path:        publication.Path,
			DisplayName: publication.DisplayNameOr(defaultDisplayName(publication)),
			EntryPoint:  publication.EntryPoint,
			RoutingMode: publication.RoutingMode,
		}
		var newest time.Time
		for _, key := range publication.Objects {
			listed, ok := observed[key]
			if !ok {
				return Plan{}, fmt.Errorf("declared object %q does not exist in storage", key)
			}
			content, err := reader.ReadObject(ctx, key)
			if err != nil {
				return Plan{}, fmt.Errorf("read declared object %q: %w", key, err)
			}
			modified := content.LastModified
			if modified.IsZero() {
				modified = listed.LastModified
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
			if modified.After(newest) {
				newest = modified
			}
		}
		sortObjects(planned.Objects)
		planned.ContentChangedAt = newest.UTC().Format(time.RFC3339)
		plan.Publications = append(plan.Publications, planned)
	}

	digest, err := Digest(plan)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Content: plan, Digest: digest}, nil
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
