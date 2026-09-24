package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
)

func TestPublicationPreviewReturnsObservedState(t *testing.T) {
	store, result := openSeededStore(t)
	publicationID := result.Projects[0].Publications[0].PublicationID

	drifted := int64(999)
	if err := store.RecordObservation(catalog.ObservationRecord{
		ObservedAt:     time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		MutationLock:   catalog.LockNone,
		TotalBytes:     999,
		AcceptedBytes:  150,
		UnclaimedBytes: 0,
		Publications: []catalog.PublicationObservationRecord{{
			PublicationID: publicationID,
			State:         catalog.StateDrifted,
			ObservedSize:  &drifted,
			StatusDetail:  "entry point bytes changed",
		}},
	}); err != nil {
		t.Fatalf("record observation: %v", err)
	}

	preview, found, err := store.PublicationPreview(context.Background(), "notes/2026-report")
	if err != nil || !found {
		t.Fatalf("PublicationPreview() = %v, found %v, err %v", preview, found, err)
	}
	if preview.Path != "notes/2026-report" || preview.DisplayName != "2026 Report" {
		t.Fatalf("unexpected preview identity: %+v", preview)
	}
	if preview.Observation == nil {
		t.Fatal("PublicationPreview() returned no observation for an observed publication")
	}
	if preview.Observation.State != catalog.StateDrifted || preview.Observation.StatusDetail != "entry point bytes changed" {
		t.Fatalf("unexpected observation: %+v", preview.Observation)
	}
	if !preview.ObservedAt.Equal(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ObservedAt = %v, want the latest observation time", preview.ObservedAt)
	}
}

func TestPublicationPreviewWithoutObservation(t *testing.T) {
	store, _ := openSeededStore(t)

	preview, found, err := store.PublicationPreview(context.Background(), "notes/2026-report")
	if err != nil || !found {
		t.Fatalf("PublicationPreview() = %v, found %v, err %v", preview, found, err)
	}
	if preview.Observation != nil {
		t.Fatalf("Observation = %+v, want nil before the first scan", preview.Observation)
	}
}

func TestPublicationPreviewUnknownPath(t *testing.T) {
	store, _ := openSeededStore(t)

	if _, found, err := store.PublicationPreview(context.Background(), "notes/absent"); err != nil || found {
		t.Fatalf("PublicationPreview() found %v, err %v, want not found", found, err)
	}
}

// TestPublicationPreviewUsesTheLatestObservation pins that the preview reads
// the newest complete observation, matching the inventory.
func TestPublicationPreviewUsesTheLatestObservation(t *testing.T) {
	store, result := openSeededStore(t)
	publicationID := result.Projects[0].Publications[0].PublicationID

	for _, record := range []catalog.ObservationRecord{
		{
			ObservedAt:   time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
			MutationLock: catalog.LockNone,
			Publications: []catalog.PublicationObservationRecord{{
				PublicationID: publicationID,
				State:         catalog.StateDrifted,
				StatusDetail:  "older finding",
			}},
		},
		{
			ObservedAt:   time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
			MutationLock: catalog.LockNone,
			Publications: []catalog.PublicationObservationRecord{{
				PublicationID: publicationID,
				State:         catalog.StateInSync,
			}},
		},
	} {
		if err := store.RecordObservation(record); err != nil {
			t.Fatalf("record observation: %v", err)
		}
	}

	preview, found, err := store.PublicationPreview(context.Background(), "notes/2026-report")
	if err != nil || !found {
		t.Fatalf("PublicationPreview() = %v, found %v, err %v", preview, found, err)
	}
	if preview.Observation == nil || preview.Observation.State != catalog.StateInSync {
		t.Fatalf("Observation = %+v, want the newest in-sync result", preview.Observation)
	}
}
