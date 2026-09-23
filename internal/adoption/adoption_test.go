package adoption

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

// singleDeclaration declares one directory-index Publication with two
// objects, matching the first adoption checkpoint.
func singleDeclaration() Declaration {
	return Declaration{
		Version: 1,
		Publications: []PublicationDeclaration{{
			Project:     ProjectDeclaration{Prefix: "notes", DisplayName: "Notes"},
			Path:        "notes/2026-report",
			DisplayName: "2026 Report",
			EntryPoint:  "notes/2026-report/index.html",
			RoutingMode: RoutingDirectoryIndex,
			Objects:     []string{"notes/2026-report/index.html", "notes/2026-report/style.css"},
		}},
	}
}

func fakeBucket(t *testing.T, objects map[string]s3test.Object) (*s3test.Server, storage.Reader) {
	t.Helper()
	bucket := s3test.NewServer("page-hub", objects)
	t.Cleanup(bucket.Close)
	reader := storage.NewS3Reader(storage.Config{
		Endpoint:        bucket.URL(),
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	return bucket, reader
}

func baseObjects() map[string]s3test.Object {
	return map[string]s3test.Object{
		"notes/2026-report/index.html": {
			Content:      []byte("<html><body>2026 report</body></html>"),
			ContentType:  "text/html; charset=utf-8",
			UserMetadata: map[string]string{"source": "legacy-share"},
			LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ETag:         `"etag-html"`,
		},
		"notes/2026-report/style.css": {
			Content:      []byte("body { color: black }"),
			ContentType:  "text/css",
			LastModified: time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC),
			ETag:         `"etag-css"`,
		},
	}
}

func migratedCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	if _, _, err := catalog.Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return path
}

func assertOnlyReadRequests(t *testing.T, bucket *s3test.Server) {
	t.Helper()
	for _, request := range bucket.Requests() {
		switch request.Method {
		case http.MethodGet, http.MethodHead:
		default:
			t.Fatalf("storage request %s %s mutates storage", request.Method, request.Path)
		}
	}
}

func TestDeclarationValidation(t *testing.T) {
	if err := singleDeclaration().Validate(); err != nil {
		t.Fatalf("valid declaration rejected: %v", err)
	}

	cases := map[string]func(*Declaration){
		"reserved manager prefix": func(d *Declaration) { d.Publications[0].Project.Prefix = "_page-hub" },
		"reserved prefix case":    func(d *Declaration) { d.Publications[0].Project.Prefix = "_PAGE-HUB" },
		"prefix with slash":       func(d *Declaration) { d.Publications[0].Project.Prefix = "nested/prefix" },
		"path outside prefix":     func(d *Declaration) { d.Publications[0].Path = "elsewhere/report" },
		"entry point not declared": func(d *Declaration) {
			d.Publications[0].EntryPoint = "notes/2026-report/missing.html"
		},
		"exact file with two objects": func(d *Declaration) {
			d.Publications[0].RoutingMode = RoutingExactFile
		},
		"duplicate object keys": func(d *Declaration) {
			d.Publications[0].Objects = append(d.Publications[0].Objects, d.Publications[0].Objects[0])
		},
		"empty object set": func(d *Declaration) { d.Publications[0].Objects = nil },
		"invalid routing mode": func(d *Declaration) {
			d.Publications[0].RoutingMode = RoutingMode("sync")
		},
		"object outside publication path": func(d *Declaration) {
			d.Publications[0].Objects = append(d.Publications[0].Objects, "other/file.html")
		},
		"empty version":   func(d *Declaration) { d.Version = 2 },
		"no publications": func(d *Declaration) { d.Publications = nil },
	}
	for name, mutate := range cases {
		declaration := singleDeclaration()
		mutate(&declaration)
		if err := declaration.Validate(); err == nil {
			t.Errorf("%s: declaration should be rejected", name)
		}
	}
}

func TestDeclarationPreservesExactCase(t *testing.T) {
	declaration := Declaration{
		Version: 1,
		Publications: []PublicationDeclaration{{
			Project:     ProjectDeclaration{Prefix: "docs"},
			Path:        "docs/Annual-Report.TXT",
			EntryPoint:  "docs/Annual-Report.TXT",
			RoutingMode: RoutingExactFile,
			Objects:     []string{"docs/Annual-Report.TXT"},
		}},
	}
	if err := declaration.Validate(); err != nil {
		t.Fatalf("uppercase exact-file declaration rejected: %v", err)
	}
	if got := RelativePath("docs/Annual-Report.TXT", "docs/Annual-Report.TXT"); got != "Annual-Report.TXT" {
		t.Fatalf("relative path = %q", got)
	}
}

func TestPlanRecordsExactObservedState(t *testing.T) {
	bucket, reader := fakeBucket(t, baseObjects())

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := plan.Verify(); err != nil {
		t.Fatalf("plan digest does not verify: %v", err)
	}

	publication := plan.Content.Publications[0]
	if publication.Path != "notes/2026-report" || publication.DisplayName != "2026 Report" {
		t.Fatalf("publication = %+v", publication)
	}
	if publication.RoutingMode != RoutingDirectoryIndex || publication.EntryPoint != "notes/2026-report/index.html" {
		t.Fatalf("publication = %+v", publication)
	}
	if publication.ContentChangedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("content changed at = %q, want the newest object modification time", publication.ContentChangedAt)
	}
	if len(publication.Objects) != 2 {
		t.Fatalf("planned %d objects, want 2", len(publication.Objects))
	}
	indexObject := baseObjects()["notes/2026-report/index.html"]
	index := publication.Objects[0]
	if index.RelativePath != "index.html" || index.Size != int64(len(indexObject.Content)) || index.ETag != `"etag-html"` {
		t.Fatalf("index object = %+v", index)
	}
	if index.ContentType != "text/html; charset=utf-8" || index.UserMetadata["source"] != "legacy-share" {
		t.Fatalf("index object = %+v", index)
	}
	if len(index.SHA256) != 64 {
		t.Fatalf("index object sha256 = %q", index.SHA256)
	}

	assertOnlyReadRequests(t, bucket)
}

func TestPlanDigestBindsEveryObservedFact(t *testing.T) {
	_, reader := fakeBucket(t, baseObjects())
	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Planning the identical declaration against identical storage twice
	// produces the identical digest.
	bucket2, reader2 := fakeBucket(t, baseObjects())
	again, err := BuildPlan(context.Background(), reader2, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() again error = %v", err)
	}
	_ = bucket2
	if again.Digest != plan.Digest {
		t.Fatal("identical storage produced different plan digests")
	}

	drifts := map[string]func(objects map[string]s3test.Object){
		"changed byte": func(objects map[string]s3test.Object) {
			objects["notes/2026-report/index.html"] = s3test.Object{
				Content:      []byte("<html><body>2027 report</body></html>"),
				ContentType:  "text/html; charset=utf-8",
				LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				ETag:         `"etag-html"`,
			}
		},
		"changed metadata": func(objects map[string]s3test.Object) {
			objects["notes/2026-report/index.html"] = baseObjects()["notes/2026-report/index.html"]
			object := objects["notes/2026-report/index.html"]
			object.UserMetadata = map[string]string{"source": "changed"}
			objects["notes/2026-report/index.html"] = object
		},
		"changed etag": func(objects map[string]s3test.Object) {
			object := objects["notes/2026-report/style.css"]
			object.ETag = `"etag-css-v2"`
			objects["notes/2026-report/style.css"] = object
		},
		"changed modification time": func(objects map[string]s3test.Object) {
			object := objects["notes/2026-report/style.css"]
			object.LastModified = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			objects["notes/2026-report/style.css"] = object
		},
	}
	for name, drift := range drifts {
		objects := baseObjects()
		drift(objects)
		_, driftedReader := fakeBucket(t, objects)
		drifted, err := BuildPlan(context.Background(), driftedReader, singleDeclaration())
		if err != nil {
			t.Fatalf("%s: Plan() error = %v", name, err)
		}
		if drifted.Digest == plan.Digest {
			t.Fatalf("%s: drifted storage produced the approved digest", name)
		}
	}
}

func TestPlanRejectsMissingDeclaredObject(t *testing.T) {
	objects := baseObjects()
	delete(objects, "notes/2026-report/style.css")
	_, reader := fakeBucket(t, objects)

	if _, err := BuildPlan(context.Background(), reader, singleDeclaration()); err == nil {
		t.Fatal("planning a missing declared object should fail")
	}
}

func TestCommitAcceptsAndPersistsState(t *testing.T) {
	bucket, reader := fakeBucket(t, baseObjects())
	catalogPath := migratedCatalog(t)

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	operationID := catalog.NewID()
	result, err := Commit(context.Background(), reader, plan, operationID, catalogPath)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result.Replayed {
		t.Fatal("a fresh commit must not be a replay")
	}
	if result.Result.Status != "committed" || result.Result.ProjectPrefix != "notes" {
		t.Fatalf("result = %+v", result.Result)
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 1 || inventory[0].Prefix != "notes" || len(inventory[0].Publications) != 1 {
		t.Fatalf("inventory = %+v", inventory)
	}
	indexObject, styleObject := baseObjects()["notes/2026-report/index.html"], baseObjects()["notes/2026-report/style.css"]
	expectedSize := int64(len(indexObject.Content) + len(styleObject.Content))
	publication := inventory[0].Publications[0]
	if publication.Path != "notes/2026-report" || publication.Size != expectedSize || publication.RoutingMode != "directory_index" {
		t.Fatalf("publication = %+v (want size %d)", publication, expectedSize)
	}

	assertOnlyReadRequests(t, bucket)
}

func TestCommitReplaysIdenticalOperation(t *testing.T) {
	bucket, reader := fakeBucket(t, baseObjects())
	catalogPath := migratedCatalog(t)

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	operationID := catalog.NewID()
	first, err := Commit(context.Background(), reader, plan, operationID, catalogPath)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	requestsBeforeReplay := len(bucket.Requests())
	replay, err := Commit(context.Background(), reader, plan, operationID, catalogPath)
	if err != nil {
		t.Fatalf("Commit() replay error = %v", err)
	}
	if !replay.Replayed {
		t.Fatal("reused operation ID should return the durable result")
	}
	if replay.Result != first.Result {
		t.Fatalf("replay result %+v differs from original %+v", replay.Result, first.Result)
	}
	if len(bucket.Requests()) != requestsBeforeReplay {
		t.Fatal("a replay must not touch storage")
	}

	// Same operation ID with a different, valid plan fails.
	changed := baseObjects()
	changed["notes/2026-report/index.html"] = s3test.Object{
		Content:      []byte("<html><body>rewritten</body></html>"),
		ContentType:  "text/html; charset=utf-8",
		LastModified: time.Date(2026, 3, 3, 3, 4, 5, 0, time.UTC),
		ETag:         `"etag-html-v2"`,
	}
	_, changedReader := fakeBucket(t, changed)
	otherPlan, err := BuildPlan(context.Background(), changedReader, singleDeclaration())
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if otherPlan.Digest == plan.Digest {
		t.Fatal("expected a different plan digest")
	}
	if _, err := Commit(context.Background(), changedReader, otherPlan, operationID, catalogPath); err == nil {
		t.Fatal("operation ID reuse with different content should fail")
	}
}

func TestCommitRejectsDriftedStorage(t *testing.T) {
	bucket, reader := fakeBucket(t, baseObjects())
	catalogPath := migratedCatalog(t)

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Storage changes between approval and commit.
	bucket.SetObject("notes/2026-report/index.html", s3test.Object{
		Content:      []byte("<html><body>changed</body></html>"),
		ContentType:  "text/html; charset=utf-8",
		LastModified: time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC),
		ETag:         `"etag-html"`,
	})

	if _, err := Commit(context.Background(), reader, plan, catalog.NewID(), catalogPath); err == nil {
		t.Fatal("committing drifted storage should fail")
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 0 {
		t.Fatalf("a rejected commit left %d projects behind", len(inventory))
	}
}

func TestCommitRefusesWhileRuntimeHoldsCatalog(t *testing.T) {
	_, reader := fakeBucket(t, baseObjects())
	catalogPath := migratedCatalog(t)

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Simulate the running manager holding the catalog.
	lock, err := catalog.AcquireLock(catalogPath)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	defer lock.Release()

	if _, err := Commit(context.Background(), reader, plan, catalog.NewID(), catalogPath); err == nil {
		t.Fatal("commit must refuse while the runtime holds the catalog")
	}
}

func TestCommitRefusesUnmigratedCatalog(t *testing.T) {
	_, reader := fakeBucket(t, baseObjects())
	catalogPath := filepath.Join(t.TempDir(), "fresh.db")

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := Commit(context.Background(), reader, plan, catalog.NewID(), catalogPath); err == nil {
		t.Fatal("commit must refuse an unmigrated catalog")
	}
}

func TestCommitRejectsAlreadyManagedPaths(t *testing.T) {
	_, reader := fakeBucket(t, baseObjects())
	catalogPath := migratedCatalog(t)

	plan, err := BuildPlan(context.Background(), reader, singleDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := Commit(context.Background(), reader, plan, catalog.NewID(), catalogPath); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}

	// A different operation ID for the same already-managed state fails
	// closed instead of duplicating catalog records.
	if _, err := Commit(context.Background(), reader, plan, catalog.NewID(), catalogPath); err == nil {
		t.Fatal("committing an already managed publication with a new operation ID should fail")
	}
}
