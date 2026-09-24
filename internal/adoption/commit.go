package adoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// CommitResult reports what happened when a plan was committed.
type CommitResult struct {
	Result   catalog.AdoptionResult
	Replayed bool
}

// CommitInput carries everything one commit needs. The approved plan binds
// the public base URL; ExpectedPublicBaseURL, when set, must match it so a
// commit can never recheck the batch against a different origin than the one
// the operator approved.
type CommitInput struct {
	Reader                storage.Reader
	Prober                RouteProber
	Approved              Plan
	OperationID           string
	CatalogPath           string
	ExpectedPublicBaseURL string
}

// Commit accepts an approved batch plan into the catalog:
//
//  1. verifies the approved plan document is intact (digest over content);
//  2. takes the exclusive catalog lock, refusing to run while the Page Hub
//     runtime holds the catalog;
//  3. returns the durable result when the operation ID was already committed
//     with identical content, and fails when its content differs;
//  4. rechecks the complete batch against storage and the public routes
//     immediately before the catalog transaction, rejecting the plan when
//     any object, metadata value, key set, or probe result changed;
//  5. accepts every declared Project, Publication, and immutable initial
//     manifest in exactly one transaction.
//
// Commit never issues a storage write, copy, or delete request.
func Commit(ctx context.Context, input CommitInput) (CommitResult, error) {
	approved := input.Approved
	if err := approved.Verify(); err != nil {
		return CommitResult{}, fmt.Errorf("approved plan is not intact: %w", err)
	}
	if err := approved.Content.Declaration.Validate(); err != nil {
		return CommitResult{}, fmt.Errorf("approved declaration is invalid: %w", err)
	}
	if input.OperationID == "" {
		return CommitResult{}, fmt.Errorf("an operation ID is required")
	}
	if input.Reader == nil || input.Prober == nil {
		return CommitResult{}, fmt.Errorf("a storage reader and public-route prober are required")
	}
	if input.ExpectedPublicBaseURL != "" && input.ExpectedPublicBaseURL != approved.Content.PublicBaseURL {
		return CommitResult{}, fmt.Errorf(
			"approved plan was generated for public base URL %s, but this command is configured for %s; generate a new plan against the configured origin",
			approved.Content.PublicBaseURL, input.ExpectedPublicBaseURL)
	}

	lock, err := catalog.AcquireLock(input.CatalogPath)
	if err != nil {
		return CommitResult{}, err
	}
	defer lock.Release()

	store, err := catalog.OpenRuntime(input.CatalogPath)
	if err != nil {
		return CommitResult{}, err
	}
	defer store.Close()

	requestHash := RequestHash(input.OperationID, approved.Digest)
	record, found, err := store.FindOperation(input.OperationID)
	if err != nil {
		return CommitResult{}, err
	}
	if found {
		if record.RequestHash != requestHash {
			return CommitResult{}, fmt.Errorf("operation ID %q was already used with different content", input.OperationID)
		}
		var previous catalog.AdoptionResult
		if err := json.Unmarshal([]byte(record.ResultJSON), &previous); err != nil {
			return CommitResult{}, fmt.Errorf("decode recorded operation result: %w", err)
		}
		return CommitResult{Result: previous, Replayed: true}, nil
	}

	// Recheck the complete batch immediately before the transaction: any
	// changed byte, metadata value, key set, public-probe result, or
	// declaration invalidates the plan.
	rechecked, err := BuildPlan(ctx, input.Reader, input.Prober, approved.Content.PublicBaseURL, approved.Content.Declaration)
	if err != nil {
		return CommitResult{}, fmt.Errorf("recheck against storage and public routes failed: %w", err)
	}
	if rechecked.Digest != approved.Digest {
		return CommitResult{}, fmt.Errorf("storage or public routes drifted from the approved plan (approved %s, observed %s)", approved.Digest, rechecked.Digest)
	}

	result, err := store.CommitAdoptionBatch(catalog.CommitAdoptionBatchInput{
		OperationID: input.OperationID,
		RequestHash: requestHash,
		PlanDigest:  approved.Digest,
		Projects:    adoptProjects(rechecked.Content.Publications),
	})
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{Result: result}, nil
}

// RequestHash binds an operation ID to the approved plan content so reusing
// the ID with different content fails.
func RequestHash(operationID, planDigest string) string {
	sum := sha256.Sum256([]byte(operationID + "\n" + planDigest))
	return hex.EncodeToString(sum[:])
}

// adoptProjects groups the planned publications by declared Project and
// carries each Project's declared display name, defaulting to the exact
// prefix.
func adoptProjects(publications []PlannedPublication) []catalog.AdoptedProject {
	byPrefix := map[string]*catalog.AdoptedProject{}
	var prefixes []string
	for _, planned := range publications {
		project, ok := byPrefix[planned.Project.Prefix]
		if !ok {
			display := planned.Project.DisplayName
			if display == "" {
				display = planned.Project.Prefix
			}
			project = &catalog.AdoptedProject{Prefix: planned.Project.Prefix, DisplayName: display}
			byPrefix[planned.Project.Prefix] = project
			prefixes = append(prefixes, planned.Project.Prefix)
		}
		project.Publications = append(project.Publications, catalog.AdoptedPublication{
			Path:             planned.Path,
			DisplayName:      planned.DisplayName,
			EntryPoint:       planned.EntryPoint,
			RoutingMode:      string(planned.RoutingMode),
			ContentChangedAt: planned.ContentChangedAt,
			Objects:          adoptObjects(planned.Objects),
		})
	}
	sort.Strings(prefixes)
	projects := make([]catalog.AdoptedProject, 0, len(prefixes))
	for _, prefix := range prefixes {
		projects = append(projects, *byPrefix[prefix])
	}
	return projects
}

func adoptObjects(objects []PlannedObject) []catalog.AdoptedObject {
	adopted := make([]catalog.AdoptedObject, 0, len(objects))
	for _, object := range objects {
		adopted = append(adopted, catalog.AdoptedObject{
			Key:             object.Key,
			RelativePath:    object.RelativePath,
			Size:            object.Size,
			ETag:            object.ETag,
			ModifiedAt:      object.ModifiedAt,
			ContentType:     object.ContentType,
			ContentEncoding: object.ContentEncoding,
			CacheControl:    object.CacheControl,
			UserMetadata:    object.UserMetadata,
			SHA256:          object.SHA256,
		})
	}
	return adopted
}

// EncodePlan renders a plan as reviewable JSON ending in a newline.
func EncodePlan(plan Plan) ([]byte, error) {
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}
	return append(encoded, '\n'), nil
}

// DecodePlan parses a plan document and verifies its digest.
func DecodePlan(encoded []byte) (Plan, error) {
	var plan Plan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return Plan{}, fmt.Errorf("parse plan: %w", err)
	}
	if plan.Digest == "" {
		return Plan{}, errors.New("plan is missing its digest")
	}
	return plan, nil
}
