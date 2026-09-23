package adoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// CommitResult reports what happened when a plan was committed.
type CommitResult struct {
	Result   catalog.AdoptionResult
	Replayed bool
}

// Commit accepts an approved plan into the catalog:
//
//  1. verifies the approved plan document is intact (digest over content);
//  2. takes the exclusive catalog lock, refusing to run while the Page Hub
//     runtime holds the catalog;
//  3. returns the durable result when the operation ID was already committed
//     with identical content, and fails when its content differs;
//  4. rechecks the candidate against storage immediately before the catalog
//     transaction, rejecting the plan when anything changed;
//  5. accepts the declared Project, Publication, and immutable initial
//     manifest in exactly one transaction.
//
// Commit never issues a storage write, copy, or delete request.
func Commit(ctx context.Context, reader storage.Reader, approved Plan, operationID, catalogPath string) (CommitResult, error) {
	if err := approved.Verify(); err != nil {
		return CommitResult{}, fmt.Errorf("approved plan is not intact: %w", err)
	}
	if err := approved.Content.Declaration.Validate(); err != nil {
		return CommitResult{}, fmt.Errorf("approved declaration is invalid: %w", err)
	}
	if len(approved.Content.Publications) != 1 {
		return CommitResult{}, fmt.Errorf("this release adopts exactly one declared publication, plan declares %d", len(approved.Content.Publications))
	}
	if operationID == "" {
		return CommitResult{}, fmt.Errorf("an operation ID is required")
	}

	lock, err := catalog.AcquireLock(catalogPath)
	if err != nil {
		return CommitResult{}, err
	}
	defer lock.Release()

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		return CommitResult{}, err
	}
	defer store.Close()

	requestHash := RequestHash(operationID, approved.Digest)
	record, found, err := store.FindOperation(operationID)
	if err != nil {
		return CommitResult{}, err
	}
	if found {
		if record.RequestHash != requestHash {
			return CommitResult{}, fmt.Errorf("operation ID %q was already used with different content", operationID)
		}
		var previous catalog.AdoptionResult
		if err := json.Unmarshal([]byte(record.ResultJSON), &previous); err != nil {
			return CommitResult{}, fmt.Errorf("decode recorded operation result: %w", err)
		}
		return CommitResult{Result: previous, Replayed: true}, nil
	}

	// Recheck the complete candidate immediately before the transaction: any
	// changed byte, metadata value, or key set invalidates the plan.
	rechecked, err := BuildPlan(ctx, reader, approved.Content.Declaration)
	if err != nil {
		return CommitResult{}, fmt.Errorf("recheck against storage failed: %w", err)
	}
	if rechecked.Digest != approved.Digest {
		return CommitResult{}, fmt.Errorf("storage drifted from the approved plan (approved %s, observed %s)", approved.Digest, rechecked.Digest)
	}

	planned := rechecked.Content.Publications[0]
	result, err := store.CommitAdoption(catalog.CommitAdoptionInput{
		OperationID: operationID,
		RequestHash: requestHash,
		PlanDigest:  approved.Digest,
		Publication: catalog.AdoptedPublication{
			ProjectPrefix:    planned.Project.Prefix,
			ProjectDisplay:   displayNameOr(planned),
			PublicationPath:  planned.Path,
			DisplayName:      planned.DisplayName,
			EntryPoint:       planned.EntryPoint,
			RoutingMode:      string(planned.RoutingMode),
			ContentChangedAt: planned.ContentChangedAt,
			Objects:          adoptObjects(planned.Objects),
		},
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

func displayNameOr(planned PlannedPublication) string {
	if planned.DisplayName != "" {
		return planned.DisplayName
	}
	return planned.Project.Prefix
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
