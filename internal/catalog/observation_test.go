package catalog_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
)

// openSeededStore migrates a fresh catalog, adopts one deterministic batch,
// and returns the open store plus the adopted publication IDs.
func openSeededStore(t *testing.T) (*catalog.Store, catalog.AdoptionResult) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-catalog.db")
	if _, _, err := catalog.Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := catalog.OpenRuntime(path)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	result, err := store.CommitAdoptionBatch(catalog.CommitAdoptionBatchInput{
		OperationID: "00000000-0000-4000-8000-0000000000a1",
		RequestHash: "test-only",
		PlanDigest:  "sha256:test-only",
		Projects: []catalog.AdoptedProject{{
			Prefix:      "notes",
			DisplayName: "notes",
			Publications: []catalog.AdoptedPublication{{
				Path:             "notes/2026-report",
				DisplayName:      "2026 Report",
				EntryPoint:       "notes/2026-report/index.html",
				RoutingMode:      "directory_index",
				ContentChangedAt: "2026-01-02T03:04:05Z",
				Objects: []catalog.AdoptedObject{
					{
						Key: "notes/2026-report/index.html", RelativePath: "index.html",
						Size: 120, ETag: `"etag-html"`, ModifiedAt: "2026-01-02T03:04:05Z",
						ContentType: "text/html", SHA256: "aa",
						UserMetadata: map[string]string{"source": "legacy"},
					},
					{
						Key: "notes/2026-report/style.css", RelativePath: "style.css",
						Size: 30, ETag: `"etag-css"`, ModifiedAt: "2025-12-31T10:00:00Z",
						ContentType: "text/css", SHA256: "bb",
					},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("seed adoption: %v", err)
	}
	return store, result
}

func TestAcceptedManifestsReturnsCurrentManifestObjects(t *testing.T) {
	store, adoption := openSeededStore(t)
	defer store.Close()

	manifests, err := store.AcceptedManifests(context.Background())
	if err != nil {
		t.Fatalf("AcceptedManifests() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1", len(manifests))
	}
	manifest := manifests[0]
	if manifest.PublicationID != adoption.Projects[0].Publications[0].PublicationID {
		t.Fatalf("manifest publication = %q, want %q", manifest.PublicationID, adoption.Projects[0].Publications[0].PublicationID)
	}
	if manifest.Path != "notes/2026-report" {
		t.Fatalf("manifest path = %q", manifest.Path)
	}
	if len(manifest.Objects) != 2 {
		t.Fatalf("manifest objects = %d, want 2", len(manifest.Objects))
	}
	first := manifest.Objects[0]
	if first.Key != "notes/2026-report/index.html" || first.Size != 120 || first.ETag != `"etag-html"` || first.SHA256 != "aa" {
		t.Fatalf("first object = %+v", first)
	}
	if first.UserMetadata["source"] != "legacy" {
		t.Fatalf("first object metadata = %+v", first.UserMetadata)
	}
}

func TestRecordObservationStoresCompleteBucketObservation(t *testing.T) {
	store, adoption := openSeededStore(t)
	defer store.Close()
	publicationID := adoption.Projects[0].Publications[0].PublicationID
	observedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	missing := int64(0)
	observedSize := int64(150)
	err := store.RecordObservation(catalog.ObservationRecord{
		ObservedAt:     observedAt,
		MutationLock:   "global",
		TotalBytes:     400,
		AcceptedBytes:  150,
		UnclaimedBytes: 250,
		UnclaimedKeys:  []string{"stray/abandoned.html"},
		Publications: []catalog.PublicationObservationRecord{
			{PublicationID: publicationID, State: "drifted", ObservedSize: &observedSize, StatusDetail: "1 of 2 object(s) differ"},
			{PublicationID: "00000000-0000-4000-8000-0000000000zz", State: "missing", ObservedSize: &missing, StatusDetail: "absent"},
		},
	})
	if err == nil {
		t.Fatal("RecordObservation() with an unknown publication should fail")
	}
	if _, ok, err := store.LatestObservation(context.Background()); err != nil || ok {
		t.Fatalf("failed recording left an observation behind: ok=%v err=%v", ok, err)
	}

	err = store.RecordObservation(catalog.ObservationRecord{
		ObservedAt:     observedAt,
		MutationLock:   "global",
		TotalBytes:     400,
		AcceptedBytes:  150,
		UnclaimedBytes: 250,
		UnclaimedKeys:  []string{"stray/abandoned.html"},
		Publications: []catalog.PublicationObservationRecord{
			{PublicationID: publicationID, State: "drifted", ObservedSize: &observedSize, StatusDetail: "1 of 2 object(s) differ"},
		},
	})
	if err != nil {
		t.Fatalf("RecordObservation() error = %v", err)
	}
	if err := store.RecordObservation(catalog.ObservationRecord{ObservedAt: observedAt, MutationLock: "sometimes", TotalBytes: 1}); err == nil {
		t.Fatal("RecordObservation() with an invalid mutation lock should fail")
	}
	if err := store.RecordObservation(catalog.ObservationRecord{ObservedAt: observedAt, MutationLock: "none", TotalBytes: 1, Publications: []catalog.PublicationObservationRecord{{PublicationID: publicationID, State: "changed"}}}); err == nil {
		t.Fatal("RecordObservation() with an invalid publication state should fail")
	}

	observation, ok, err := store.LatestObservation(context.Background())
	if err != nil || !ok {
		t.Fatalf("LatestObservation() = %v, ok %v", err, ok)
	}
	if !observation.ObservedAt.Equal(observedAt) {
		t.Fatalf("observedAt = %v, want %v", observation.ObservedAt, observedAt)
	}
	if observation.MutationLock != "global" || observation.Usage.TotalBytes != 400 || observation.Usage.AcceptedBytes != 150 || observation.Usage.UnclaimedBytes != 250 {
		t.Fatalf("observation = %+v", observation)
	}
	if len(observation.UnclaimedKeys) != 1 || observation.UnclaimedKeys[0] != "stray/abandoned.html" {
		t.Fatalf("unclaimed keys = %+v", observation.UnclaimedKeys)
	}
	row, ok := observation.Publications[publicationID]
	if !ok || row.State != "drifted" || row.ObservedSize == nil || *row.ObservedSize != 150 || row.StatusDetail != "1 of 2 object(s) differ" {
		t.Fatalf("publication observation = %+v ok %v", row, ok)
	}
}

func TestLatestObservationReturnsNewestCompleteObservation(t *testing.T) {
	store, adoption := openSeededStore(t)
	defer store.Close()
	publicationID := adoption.Projects[0].Publications[0].PublicationID

	observation, ok, err := store.LatestObservation(context.Background())
	if err != nil || ok || observation.ObservationID != "" {
		t.Fatalf("empty catalog observation = %+v ok %v err %v", observation, ok, err)
	}

	inSync := int64(150)
	early := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	// Same-second rows order by insertion, newest insertion wins.
	sameSecond := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	for _, record := range []catalog.ObservationRecord{
		{ObservedAt: later, MutationLock: "none", TotalBytes: 150, AcceptedBytes: 150, Publications: []catalog.PublicationObservationRecord{{PublicationID: publicationID, State: "in_sync", ObservedSize: &inSync}}},
		{ObservedAt: early, MutationLock: "global", TotalBytes: 999},
		{ObservedAt: sameSecond, MutationLock: "none", TotalBytes: 151, AcceptedBytes: 150, UnclaimedBytes: 1},
		{ObservedAt: sameSecond, MutationLock: "none", TotalBytes: 152, AcceptedBytes: 150, UnclaimedBytes: 2},
	} {
		if err := store.RecordObservation(record); err != nil {
			t.Fatalf("RecordObservation(%v) error = %v", record.ObservedAt, err)
		}
	}

	observation, ok, err = store.LatestObservation(context.Background())
	if err != nil || !ok {
		t.Fatalf("LatestObservation() = %v ok %v", err, ok)
	}
	if observation.Usage.TotalBytes != 152 {
		t.Fatalf("latest observation = %+v, want the newest same-second row", observation)
	}
	if len(observation.Publications) != 0 {
		t.Fatalf("latest observation has stale publication rows: %+v", observation.Publications)
	}
}

func TestInventoryIncludesObservationState(t *testing.T) {
	store, adoption := openSeededStore(t)
	defer store.Close()
	publicationID := adoption.Projects[0].Publications[0].PublicationID
	ctx := context.Background()

	inventory, err := store.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if inventory.Observation != nil {
		t.Fatalf("inventory without scans has an observation: %+v", inventory.Observation)
	}
	if inventory.Projects[0].Publications[0].Observation != nil {
		t.Fatalf("publication without scans has an observation: %+v", inventory.Projects[0].Publications[0].Observation)
	}

	observedSize := int64(150)
	if err := store.RecordObservation(catalog.ObservationRecord{
		ObservedAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		MutationLock:   "none",
		TotalBytes:     150,
		AcceptedBytes:  150,
		UnclaimedBytes: 0,
		Publications: []catalog.PublicationObservationRecord{
			{PublicationID: publicationID, State: "in_sync", ObservedSize: &observedSize},
		},
	}); err != nil {
		t.Fatalf("RecordObservation() error = %v", err)
	}

	inventory, err = store.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if inventory.Observation == nil || inventory.Observation.MutationLock != "none" || inventory.Observation.Usage.TotalBytes != 150 {
		t.Fatalf("bucket observation = %+v", inventory.Observation)
	}
	publication := inventory.Projects[0].Publications[0]
	if publication.Observation == nil || publication.Observation.State != "in_sync" || publication.Observation.ObservedSize == nil || *publication.Observation.ObservedSize != 150 {
		t.Fatalf("publication observation = %+v", publication.Observation)
	}
	// Accepted facts stay untouched by observations.
	if publication.Size != 150 || publication.ContentChangedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("accepted publication facts changed: %+v", publication)
	}
}
