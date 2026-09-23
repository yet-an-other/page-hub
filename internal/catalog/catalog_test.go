package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func migrateFresh(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-catalog.db")
	if _, _, err := Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return path
}

func openStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := OpenRuntime(path)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestMigrateCreatesCurrentSchema(t *testing.T) {
	path := migrateFresh(t)

	store := openStore(t, path)
	defer store.Close()

	var userVersion int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	latest, err := LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion() error = %v", err)
	}
	if userVersion != latest {
		t.Fatalf("user_version = %d, want %d", userVersion, latest)
	}
	var check string
	if err := store.db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := migrateFresh(t)
	from, to, err := Migrate(path)
	if err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if from != 1 || to != 1 {
		t.Fatalf("second Migrate() = (%d, %d), want (1, 1)", from, to)
	}
}

func TestOpenRuntimeRefusesUnmigratedCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	if _, err := OpenRuntime(path); err == nil {
		t.Fatal("OpenRuntime() on an uninitialized catalog should fail")
	} else if want := "migration"; !regexp.MustCompile(want).MatchString(err.Error()) {
		t.Fatalf("error = %v, want it to mention %q", err, want)
	}
}

func TestOpenRuntimeRefusesNewerSchema(t *testing.T) {
	path := migrateFresh(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatalf("bump user_version: %v", err)
	}
	db.Close()

	if _, err := OpenRuntime(path); err == nil {
		t.Fatal("OpenRuntime() on a newer schema should fail")
	} else if want := "compatible"; !regexp.MustCompile(want).MatchString(err.Error()) {
		t.Fatalf("error = %v, want it to mention %q", err, want)
	}
}

func TestOpenRuntimeRefusesPartiallyAppliedSchema(t *testing.T) {
	path := migrateFresh(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Simulate a crash between schema creation and history recording: tables
	// exist but the migration was never recorded.
	if _, err := db.Exec(`DELETE FROM schema_migrations; PRAGMA user_version = 0`); err != nil {
		t.Fatalf("reset history: %v", err)
	}
	db.Close()

	if _, err := OpenRuntime(path); err == nil {
		t.Fatal("OpenRuntime() on a partially applied schema should fail")
	}
}

func TestOpenRuntimeRefusesTamperedHistory(t *testing.T) {
	path := migrateFresh(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET checksum = 'tampered'`); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}
	db.Close()

	if _, err := OpenRuntime(path); err == nil {
		t.Fatal("OpenRuntime() with a tampered migration checksum should fail")
	}
}

func TestMigrateRefusesHeldCatalog(t *testing.T) {
	path := migrateFresh(t)
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	defer lock.Release()

	if _, _, err := Migrate(path); err == nil {
		t.Fatal("Migrate() while the catalog is held should fail")
	}
}

func TestAdoptCommitAndRestartPreservesState(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)

	input := CommitAdoptionInput{
		OperationID: NewID(),
		RequestHash: "request-hash-1",
		PlanDigest:  "sha256:plan-digest-1",
		Publication: AdoptPublication{
			ProjectPrefix:    "notes",
			ProjectDisplay:   "Notes",
			PublicationPath:  "notes/2026-report",
			DisplayName:      "2026 Report",
			EntryPoint:       "notes/2026-report/index.html",
			RoutingMode:      "directory_index",
			ContentChangedAt: "2026-01-02T03:04:05Z",
			Objects: []AdoptedObject{
				{
					Key: "notes/2026-report/index.html", RelativePath: "index.html",
					Size: 120, ETag: `"abc123"`, ModifiedAt: "2026-01-02T03:04:05Z",
					ContentType: "text/html", SHA256: fakeSHA("aa"),
					UserMetadata: map[string]string{"owner": "operator"},
				},
				{
					Key: "notes/2026-report/style.css", RelativePath: "style.css",
					Size: 30, ETag: `"def456"`, ModifiedAt: "2026-01-01T00:00:00Z",
					ContentType: "text/css", SHA256: fakeSHA("bb"),
				},
			},
		},
	}

	result, err := store.CommitAdoption(input)
	if err != nil {
		t.Fatalf("CommitAdoption() error = %v", err)
	}
	if result.Status != "committed" || result.ObjectCount != 2 || result.AcceptedSize != 150 {
		t.Fatalf("result = %+v", result)
	}
	if result.ProjectID == "" || result.PublicationID == "" || result.ManifestRevisionID == "" {
		t.Fatalf("result is missing identities: %+v", result)
	}

	record, found, err := store.FindOperation(input.OperationID)
	if err != nil || !found {
		t.Fatalf("FindOperation() = (%v, %v, %v)", record, found, err)
	}
	if record.RequestHash != input.RequestHash || record.PlanDigest != input.PlanDigest {
		t.Fatalf("record = %+v, want request %q plan %q", record, input.RequestHash, input.PlanDigest)
	}

	// Reopen the catalog as a restarted runtime would.
	store.Close()
	reopened := openStore(t, path)
	inventory, err := reopened.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 1 {
		t.Fatalf("inventory has %d projects, want 1", len(inventory))
	}
	project := inventory[0]
	if project.Prefix != "notes" || len(project.Publications) != 1 {
		t.Fatalf("project = %+v", project)
	}
	publication := project.Publications[0]
	if publication.Path != "notes/2026-report" || publication.Size != 150 || publication.EntryPoint != "notes/2026-report/index.html" {
		t.Fatalf("publication = %+v", publication)
	}
	if publication.ContentChangedAt != "2026-01-02T03:04:05Z" || publication.RoutingMode != "directory_index" {
		t.Fatalf("publication = %+v", publication)
	}
}

func TestCommitAdoptionIsAtomic(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)

	// Pre-record the operation ID with different content: the commit must fail
	// and leave no project, publication, or manifest behind.
	seedInput := CommitAdoptionInput{
		OperationID: "op-1", RequestHash: "seed", PlanDigest: "sha256:seed",
		Publication: AdoptPublication{
			ProjectPrefix: "seeded", ProjectDisplay: "Seeded", PublicationPath: "seeded/site",
			EntryPoint: "seeded/site/index.html", RoutingMode: "fallback",
			ContentChangedAt: "2026-01-01T00:00:00Z",
			Objects:          []AdoptedObject{{Key: "seeded/site/index.html", RelativePath: "index.html", SHA256: fakeSHA("cc")}},
		},
	}
	if _, err := store.CommitAdoption(seedInput); err != nil {
		t.Fatalf("seed CommitAdoption() error = %v", err)
	}

	clashing := CommitAdoptionInput{
		OperationID: "op-1", RequestHash: "different", PlanDigest: "sha256:other",
		Publication: seedInput.Publication,
	}
	// Same publication content but the same prefix already exists; the clash
	// must roll everything back.
	clashing.Publication.ProjectPrefix = "other-project"
	clashing.Publication.PublicationPath = "other-project/site"

	operationID := "op-2"
	duplicate := seedInput
	duplicate.OperationID = operationID
	// Make a late insert fail: reuse an existing operation ID recorded with
	// different content by inserting it first.
	if _, err := store.db.Exec(`INSERT INTO adoption_operations (operation_id, request_hash, plan_digest, status, result_json, created_at)
		VALUES ('op-2', 'x', 'y', 'committed', '{}', 'now')`); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if _, err := store.CommitAdoption(duplicate); err == nil {
		t.Fatal("CommitAdoption() with an existing operation ID should fail")
	}

	var projects, publications, manifests int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE prefix = 'seeded-x'`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("a failed commit left %d projects behind (%v)", projects, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM publications`).Scan(&publications); err != nil {
		t.Fatalf("count publications: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM manifest_revisions`).Scan(&manifests); err != nil {
		t.Fatalf("count manifests: %v", err)
	}
	if publications != 1 || manifests != 1 {
		t.Fatalf("unrelated seed state changed: %d publications, %d manifests", publications, manifests)
	}
}

func TestCommitAdoptionRejectsExistingPaths(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)
	base := CommitAdoptionInput{
		OperationID: "op-a", RequestHash: "h", PlanDigest: "sha256:d",
		Publication: AdoptPublication{
			ProjectPrefix: "docs", ProjectDisplay: "Docs", PublicationPath: "docs/handbook",
			EntryPoint: "docs/handbook/index.html", RoutingMode: "directory_index",
			ContentChangedAt: "2026-01-01T00:00:00Z",
			Objects:          []AdoptedObject{{Key: "docs/handbook/index.html", RelativePath: "index.html", SHA256: fakeSHA("dd")}},
		},
	}
	if _, err := store.CommitAdoption(base); err != nil {
		t.Fatalf("CommitAdoption() error = %v", err)
	}

	samePrefix := base
	samePrefix.OperationID = "op-b"
	samePrefix.Publication.PublicationPath = "docs/other"
	if _, err := store.CommitAdoption(samePrefix); err == nil {
		t.Fatal("adopting an existing project prefix should fail")
	}

	samePath := base
	samePath.OperationID = "op-c"
	samePath.Publication.ProjectPrefix = "other"
	if _, err := store.CommitAdoption(samePath); err == nil {
		t.Fatal("adopting an existing publication path should fail")
	}
}

func TestInventoryIncludesEmptyProjectsAndSortsCaseInsensitively(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)

	adopt := func(operationID, prefix, display, pubPath, pubDisplay string) {
		t.Helper()
		publicationPath := pubPath
		if publicationPath == "" {
			publicationPath = prefix + "/placeholder"
		}
		_, err := store.CommitAdoption(CommitAdoptionInput{
			OperationID: operationID, RequestHash: operationID, PlanDigest: "sha256:" + operationID,
			Publication: AdoptPublication{
				ProjectPrefix: prefix, ProjectDisplay: display, PublicationPath: publicationPath,
				DisplayName: pubDisplay, EntryPoint: publicationPath + "/index.html",
				RoutingMode: "directory_index", ContentChangedAt: "2026-01-01T00:00:00Z",
				Objects: []AdoptedObject{{Key: publicationPath + "/index.html", RelativePath: "index.html", Size: 5, SHA256: fakeSHA("ee")}},
			},
		})
		if err != nil {
			t.Fatalf("CommitAdoption(%s) error = %v", operationID, err)
		}
	}

	// Seeded directly: an empty Project has no publication rows.
	_, err := store.db.Exec(`INSERT INTO projects (id, prefix, display_name, description, entity_revision, created_at, updated_at)
		VALUES (?, 'alpha', 'alpha project', 'empty on purpose', 'rev', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, NewID())
	if err != nil {
		t.Fatalf("seed empty project: %v", err)
	}
	adopt("op-1", "zeta", "Zeta notes", "zeta/a-guide", "a guide")
	adopt("op-2", "beta", "Beta archive", "beta/Z-manual", "Z manual")

	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 3 {
		t.Fatalf("inventory has %d projects, want 3: %+v", len(inventory), inventory)
	}
	wantOrder := []string{"alpha project", "Beta archive", "Zeta notes"}
	for i, project := range inventory {
		if project.DisplayName != wantOrder[i] {
			t.Fatalf("project %d = %q, want %q", i, project.DisplayName, wantOrder[i])
		}
	}
	if inventory[0].Prefix != "alpha" || len(inventory[0].Publications) != 0 {
		t.Fatalf("empty project = %+v", inventory[0])
	}
	publications := inventory[2].Publications
	if len(publications) != 1 || publications[0].DisplayName != "a guide" {
		t.Fatalf("zeta publications = %+v", publications)
	}
}

func TestNewIDIsUUIDv4(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := NewID()
		if !pattern.MatchString(id) {
			t.Fatalf("NewID() = %q, want a UUIDv4", id)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = true
	}
}

func TestRuntimeRefusesConcurrentHold(t *testing.T) {
	path := migrateFresh(t)
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	defer lock.Release()

	if _, err := AcquireLock(path); err == nil {
		t.Fatal("a second lock on the same catalog should fail")
	}
}

func fakeSHA(pair string) string { return strings.Repeat(pair, 32) }
