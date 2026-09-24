package observe_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/observe"
	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

func mustSHA(t *testing.T, content string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// runFixtures seeds a catalog through the adoption path and a bucket through
// the production adapter's test server, then runs one observation.
func runFixtures(t *testing.T) (*s3test.Server, *catalog.Store, catalog.AdoptionResult) {
	t.Helper()
	bucket := s3test.NewServer("page-hub", map[string]s3test.Object{
		"guides/index.html": {
			Content:      []byte("<html><body>guides home</body></html>"),
			ContentType:  "text/html; charset=utf-8",
			UserMetadata: map[string]string{"source": "legacy-share"},
			LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ETag:         `"etag-guides-index"`,
		},
		"guides/assets/style.css": {
			Content:      []byte("body { color: black }"),
			ContentType:  "text/css",
			LastModified: time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC),
			ETag:         `"etag-style"`,
		},
		"reports/Index.html": {
			Content:      []byte("<html><body>Reports</body></html>"),
			ContentType:  "text/html",
			LastModified: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			ETag:         `"etag-reports"`,
		},
	})
	t.Cleanup(bucket.Close)

	catalogPath := t.TempDir() + "/catalog.db"
	if _, _, err := catalog.Migrate(catalogPath); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })

	result, err := store.CommitAdoptionBatch(catalog.CommitAdoptionBatchInput{
		OperationID: "00000000-0000-4000-8000-0000000000b2",
		RequestHash: "test-only",
		PlanDigest:  "sha256:test-only",
		Projects: []catalog.AdoptedProject{{
			Prefix: "guides", DisplayName: "guides",
			Publications: []catalog.AdoptedPublication{{
				Path: "guides", DisplayName: "guides", EntryPoint: "guides/index.html",
				RoutingMode: "fallback", ContentChangedAt: "2026-01-02T03:04:05Z",
				Objects: []catalog.AdoptedObject{
					{
						Key: "guides/index.html", RelativePath: "index.html",
						Size: 37, ETag: `"etag-guides-index"`, ModifiedAt: "2026-01-02T03:04:05Z",
						ContentType:  "text/html; charset=utf-8",
						UserMetadata: map[string]string{"source": "legacy-share"},
						SHA256:       mustSHA(t, "<html><body>guides home</body></html>"),
					},
					{
						Key: "guides/assets/style.css", RelativePath: "assets/style.css",
						Size: 21, ETag: `"etag-style"`, ModifiedAt: "2025-12-31T10:00:00Z",
						ContentType: "text/css",
						SHA256:      mustSHA(t, "body { color: black }"),
					},
				},
			}},
		}, {
			Prefix: "reports", DisplayName: "reports",
			Publications: []catalog.AdoptedPublication{{
				Path: "reports", DisplayName: "Index.html", EntryPoint: "reports/Index.html",
				RoutingMode: "exact_file", ContentChangedAt: "2026-02-01T00:00:00Z",
				Objects: []catalog.AdoptedObject{{
					Key: "reports/Index.html", RelativePath: "Index.html",
					Size: 33, ETag: `"etag-reports"`, ModifiedAt: "2026-02-01T00:00:00Z",
					ContentType: "text/html", SHA256: mustSHA(t, "<html><body>Reports</body></html>"),
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("seed adoption: %v", err)
	}
	return bucket, store, result
}

func TestRunRecordsBucketObservation(t *testing.T) {
	bucket, store, adoption := runFixtures(t)
	reader := storage.NewS3ReaderWithHTTPClient(storage.Config{
		Endpoint: bucket.URL(), Bucket: "page-hub",
		AccessKeyID: "test-access-key", SecretAccessKey: "test-secret-key",
	}, bucket.HTTPClient())

	result, err := observe.Run(context.Background(), reader, store, observe.Options{Now: fakeNow})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Publications) != 2 {
		t.Fatalf("publications = %d, want 2", len(result.Publications))
	}
	for _, publication := range result.Publications {
		if publication.State != catalog.StateInSync {
			t.Fatalf("publication %q state = %q, want in sync", publication.Path, publication.State)
		}
	}
	if result.MutationLock != catalog.LockNone {
		t.Fatalf("mutation lock = %q, want none", result.MutationLock)
	}

	recorded, ok, err := store.LatestObservation(context.Background())
	if err != nil || !ok {
		t.Fatalf("LatestObservation() = %v ok %v", err, ok)
	}
	if recorded.ObservedAt.Equal(fakeNow()) == false {
		t.Fatalf("observed at = %v, want %v", recorded.ObservedAt, fakeNow())
	}
	if recorded.MutationLock != catalog.LockNone {
		t.Fatalf("recorded lock = %q", recorded.MutationLock)
	}
	publicationID := adoption.Projects[0].Publications[0].PublicationID
	if row := recorded.Publications[publicationID]; row.State != catalog.StateInSync {
		t.Fatalf("recorded publication = %+v", row)
	}

	// An ordinary observation reads: it lists, heads, and never writes.
	for _, request := range bucket.Requests() {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			t.Fatalf("observation issued %s request", request.Method)
		}
	}

	// Out-of-band storage change, then a new observation: drift and lock.
	bucket.SetObject("guides/index.html", s3test.Object{
		Content:      []byte("<html><body>replaced home</body></html>"),
		ContentType:  "text/html; charset=utf-8",
		UserMetadata: map[string]string{"source": "legacy-share"},
		LastModified: time.Date(2026, 4, 4, 4, 4, 4, 0, time.UTC),
		ETag:         `"etag-replaced"`,
	})
	bucket.SetObject("stray/abandoned.html", s3test.Object{
		Content: []byte("abandoned"), LastModified: time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), ETag: `"etag-stray"`,
	})

	result, err = observe.Run(context.Background(), reader, store, observe.Options{Now: func() time.Time {
		return fakeNow().Add(time.Minute)
	}})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if result.MutationLock != catalog.LockGlobal {
		t.Fatalf("second lock = %q, want global", result.MutationLock)
	}
	if len(result.UnclaimedKeys) != 1 || result.UnclaimedKeys[0] != "stray/abandoned.html" {
		t.Fatalf("unclaimed = %v", result.UnclaimedKeys)
	}
	var guides observe.PublicationResult
	for _, publication := range result.Publications {
		if publication.Path == "guides" {
			guides = publication
		}
	}
	if guides.State != catalog.StateDrifted || guides.StatusDetail == "" {
		t.Fatalf("guides after drift = %+v", guides)
	}

	// The inventory serves the newest observation without touching accepted
	// state.
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if inventory.Observation == nil || inventory.Observation.MutationLock != catalog.LockGlobal {
		t.Fatalf("inventory observation = %+v", inventory.Observation)
	}
	if inventory.Observation.Usage.UnclaimedBytes != int64(len("abandoned")) {
		t.Fatalf("inventory usage = %+v", inventory.Observation.Usage)
	}
	for _, project := range inventory.Projects {
		for _, publication := range project.Publications {
			if publication.Path == "guides" && (publication.Observation == nil || publication.Observation.State != catalog.StateDrifted) {
				t.Fatalf("guides inventory observation = %+v", publication.Observation)
			}
			// Accepted size survives drift untouched.
			if publication.Path == "guides" && publication.Size != 58 {
				t.Fatalf("accepted size changed to %d", publication.Size)
			}
		}
	}
}

func TestRunFailsWithoutRecordingWhenStorageIsUnavailable(t *testing.T) {
	_, store, _ := runFixtures(t)
	// A reader pointed at a closed server.
	reader := storage.NewS3Reader(storage.Config{
		Endpoint: "http://127.0.0.1:1", Bucket: "page-hub",
		AccessKeyID: "test-access-key", SecretAccessKey: "test-secret-key",
	})
	if _, err := observe.Run(context.Background(), reader, store, observe.Options{}); err == nil {
		t.Fatal("Run() succeeded against unavailable storage")
	}
	if _, ok, err := store.LatestObservation(context.Background()); err != nil || ok {
		t.Fatalf("a failed scan recorded an observation: ok=%v err=%v", ok, err)
	}
}
