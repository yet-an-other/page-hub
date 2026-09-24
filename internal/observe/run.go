package observe

import (
	"context"
	"fmt"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// Run performs one complete observation against storage and records it in the
// catalog in one transaction. It issues only reads to storage and never
// changes accepted state. A storage failure fails the run without recording,
// leaving the previous observation in place for the manager to report as
// stale or unavailable.
func Run(ctx context.Context, reader storage.Reader, store *catalog.Store, options Options) (Result, error) {
	manifests, err := store.AcceptedManifests(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read accepted manifests: %w", err)
	}
	result, err := Scan(ctx, reader, manifests, options)
	if err != nil {
		return Result{}, err
	}
	if err := store.RecordObservation(result.Record()); err != nil {
		return Result{}, fmt.Errorf("record observation: %w", err)
	}
	return result, nil
}
