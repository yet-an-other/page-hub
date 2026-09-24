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
	latest, err := LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion() error = %v", err)
	}
	if from != latest || to != latest {
		t.Fatalf("second Migrate() = (%d, %d), want (%d, %d)", from, to, latest, latest)
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

func TestAdoptBatchCommitAndRestartPreservesState(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)

	input := CommitAdoptionBatchInput{
		OperationID: NewID(),
		RequestHash: "request-hash-1",
		PlanDigest:  "sha256:plan-digest-1",
		Projects: []AdoptedProject{
			{
				Prefix:      "notes",
				DisplayName: "Notes",
				Publications: []AdoptedPublication{
					{
						Path:             "notes/2026-report",
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
				},
			},
			{
				Prefix:      "docs",
				DisplayName: "docs",
				Publications: []AdoptedPublication{
					{
						Path:             "docs",
						DisplayName:      "Annual-Report.TXT",
						EntryPoint:       "docs/Annual-Report.TXT",
						RoutingMode:      "exact_file",
						ContentChangedAt: "2025-06-01T00:00:00Z",
						Objects: []AdoptedObject{
							{
								Key: "docs/Annual-Report.TXT", RelativePath: "Annual-Report.TXT",
								Size: 40, ETag: `"ghi789"`, ModifiedAt: "2025-06-01T00:00:00Z",
								ContentType: "text/plain", SHA256: fakeSHA("ee"),
							},
						},
					},
				},
			},
		},
	}

	result, err := store.CommitAdoptionBatch(input)
	if err != nil {
		t.Fatalf("CommitAdoptionBatch() error = %v", err)
	}
	if result.Status != "committed" || result.ObjectCount != 3 || result.AcceptedSize != 190 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Projects) != 2 {
		t.Fatalf("result projects = %+v", result.Projects)
	}
	for _, project := range result.Projects {
		if project.ProjectID == "" {
			t.Fatalf("result project is missing an identity: %+v", project)
		}
		for _, publication := range project.Publications {
			if publication.PublicationID == "" || publication.ManifestRevisionID == "" {
				t.Fatalf("result publication is missing an identity: %+v", publication)
			}
		}
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
	if len(inventory.Projects) != 2 {
		t.Fatalf("inventory has %d projects, want 2", len(inventory.Projects))
	}
	project := inventory.Projects[0]
	if project.Prefix != "docs" || len(project.Publications) != 1 {
		t.Fatalf("project = %+v", project)
	}
	rootPublication := project.Publications[0]
	if rootPublication.Path != "docs" || rootPublication.EntryPoint != "docs/Annual-Report.TXT" || rootPublication.RoutingMode != "exact_file" {
		t.Fatalf("root publication = %+v", rootPublication)
	}
	project = inventory.Projects[1]
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

func TestCommitAdoptionBatchAcceptsSharedProjectInOneTransaction(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)

	publication := func(path string) AdoptedPublication {
		return AdoptedPublication{
			Path: path, DisplayName: path, EntryPoint: path + "/index.html",
			RoutingMode: "fallback", ContentChangedAt: "2026-01-01T00:00:00Z",
			Objects: []AdoptedObject{{Key: path + "/index.html", RelativePath: "index.html", Size: 10, SHA256: fakeSHA(path)}},
		}
	}
	input := CommitAdoptionBatchInput{
		OperationID: "op-batch", RequestHash: "h", PlanDigest: "sha256:d",
		Projects: []AdoptedProject{{
			Prefix: "guides", DisplayName: "guides",
			Publications: []AdoptedPublication{publication("guides/a"), publication("guides/b")},
		}},
	}
	result, err := store.CommitAdoptionBatch(input)
	if err != nil {
		t.Fatalf("CommitAdoptionBatch() error = %v", err)
	}
	if len(result.Projects) != 1 || len(result.Projects[0].Publications) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.ObjectCount != 2 || result.AcceptedSize != 20 {
		t.Fatalf("result = %+v", result)
	}

	store.Close()
	reopened := openStore(t, path)
	inventory, err := reopened.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory.Projects) != 1 || inventory.Projects[0].Prefix != "guides" || len(inventory.Projects[0].Publications) != 2 {
		t.Fatalf("inventory = %+v", inventory)
	}
}

func TestCommitAdoptionBatchRejectsIncompleteInput(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)
	publication := AdoptedPublication{
		Path: "docs/handbook", EntryPoint: "docs/handbook/index.html", RoutingMode: "directory_index",
		ContentChangedAt: "2026-01-01T00:00:00Z",
		Objects:          []AdoptedObject{{Key: "docs/handbook/index.html", RelativePath: "index.html", SHA256: fakeSHA("dd")}},
	}
	cases := map[string]CommitAdoptionBatchInput{
		"no projects": {OperationID: "op-1", Projects: nil},
		"project without publications": {
			OperationID: "op-2", Projects: []AdoptedProject{{Prefix: "docs", DisplayName: "docs"}},
		},
		"project without prefix": {
			OperationID: "op-3", Projects: []AdoptedProject{{DisplayName: "docs", Publications: []AdoptedPublication{publication}}},
		},
		"duplicate project prefixes": {
			OperationID: "op-4",
			Projects: []AdoptedProject{
				{Prefix: "docs", Publications: []AdoptedPublication{publication}},
				{Prefix: "docs", Publications: []AdoptedPublication{publication}},
			},
		},
		"duplicate publication paths": {
			OperationID: "op-5",
			Projects:    []AdoptedProject{{Prefix: "docs", Publications: []AdoptedPublication{publication, publication}}},
		},
	}
	for name, input := range cases {
		if _, err := store.CommitAdoptionBatch(input); err == nil {
			t.Errorf("%s: CommitAdoptionBatch() should fail", name)
		}
	}
	var projects int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("rejected batches left %d projects behind (%v)", projects, err)
	}
}

func TestCommitAdoptionBatchIsAtomic(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)

	publication := func(prefix, name string) AdoptedPublication {
		path := prefix + "/" + name
		return AdoptedPublication{
			Path: path, EntryPoint: path + "/index.html", RoutingMode: "fallback",
			ContentChangedAt: "2026-01-01T00:00:00Z",
			Objects:          []AdoptedObject{{Key: path + "/index.html", RelativePath: "index.html", SHA256: fakeSHA(name)}},
		}
	}

	// Pre-record the operation ID with different content: the commit must fail
	// and leave no project, publication, or manifest behind.
	if _, err := store.db.Exec(`INSERT INTO adoption_operations (operation_id, request_hash, plan_digest, status, result_json, created_at)
		VALUES ('op-1', 'x', 'y', 'committed', '{}', 'now')`); err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	// The batch carries one new project alongside the clashing operation ID;
	// the late failure must roll the complete batch back.
	clashing := CommitAdoptionBatchInput{
		OperationID: "op-1", RequestHash: "different", PlanDigest: "sha256:other",
		Projects: []AdoptedProject{
			{Prefix: "seeded", DisplayName: "Seeded", Publications: []AdoptedPublication{publication("seeded", "site")}},
			{Prefix: "other-project", DisplayName: "Other", Publications: []AdoptedPublication{publication("other-project", "site")}},
		},
	}
	if _, err := store.CommitAdoptionBatch(clashing); err == nil {
		t.Fatal("CommitAdoptionBatch() with an existing operation ID should fail")
	}

	var projects, publications, manifests int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("a failed batch commit left %d projects behind (%v)", projects, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM publications`).Scan(&publications); err != nil {
		t.Fatalf("count publications: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM manifest_revisions`).Scan(&manifests); err != nil {
		t.Fatalf("count manifests: %v", err)
	}
	if publications != 0 || manifests != 0 {
		t.Fatalf("a failed batch commit left state behind: %d publications, %d manifests", publications, manifests)
	}

	// A failure after partial inserts must roll the complete batch back:
	// drop the manifest table so the first manifest-object insert fails after
	// the project and publication rows are already written.
	if _, err := store.db.Exec(`DROP TABLE manifest_objects`); err != nil {
		t.Fatalf("drop manifest_objects: %v", err)
	}
	midBatch := CommitAdoptionBatchInput{
		OperationID: "op-3", RequestHash: "h", PlanDigest: "sha256:d",
		Projects: []AdoptedProject{
			{Prefix: "crash", DisplayName: "Crash", Publications: []AdoptedPublication{publication("crash", "site")}},
		},
	}
	if _, err := store.CommitAdoptionBatch(midBatch); err == nil {
		t.Fatal("a mid-transaction insert failure should fail the batch")
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("a mid-transaction failure left %d projects behind (%v)", projects, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM publications`).Scan(&publications); err != nil || publications != 0 {
		t.Fatalf("a mid-transaction failure left %d publications behind (%v)", publications, err)
	}
}

func TestCommitAdoptionBatchRejectsExistingPaths(t *testing.T) {
	path := migrateFresh(t)
	store := openStore(t, path)
	publication := func(prefix, name string) AdoptedPublication {
		path := prefix + "/" + name
		return AdoptedPublication{
			Path: path, EntryPoint: path + "/index.html", RoutingMode: "directory_index",
			ContentChangedAt: "2026-01-01T00:00:00Z",
			Objects:          []AdoptedObject{{Key: path + "/index.html", RelativePath: "index.html", SHA256: fakeSHA(name)}},
		}
	}
	base := CommitAdoptionBatchInput{
		OperationID: "op-a", RequestHash: "h", PlanDigest: "sha256:d",
		Projects: []AdoptedProject{{Prefix: "docs", DisplayName: "Docs", Publications: []AdoptedPublication{publication("docs", "handbook")}}},
	}
	if _, err := store.CommitAdoptionBatch(base); err != nil {
		t.Fatalf("CommitAdoptionBatch() error = %v", err)
	}

	// A batch mixing an already-managed prefix with a new project fails and
	// leaves the new project unaccepted.
	samePrefix := CommitAdoptionBatchInput{
		OperationID: "op-b", RequestHash: "h", PlanDigest: "sha256:d",
		Projects: []AdoptedProject{
			{Prefix: "docs", DisplayName: "Docs", Publications: []AdoptedPublication{publication("docs", "other")}},
			{Prefix: "fresh", DisplayName: "Fresh", Publications: []AdoptedPublication{publication("fresh", "site")}},
		},
	}
	if _, err := store.CommitAdoptionBatch(samePrefix); err == nil {
		t.Fatal("adopting an existing project prefix should fail")
	}

	samePath := base
	samePath.OperationID = "op-c"
	samePath.Projects = []AdoptedProject{
		{Prefix: "other", DisplayName: "Other", Publications: []AdoptedPublication{publication("docs", "handbook")}},
	}
	if _, err := store.CommitAdoptionBatch(samePath); err == nil {
		t.Fatal("adopting an existing publication path should fail")
	}

	var fresh int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE prefix = 'fresh'`).Scan(&fresh); err != nil || fresh != 0 {
		t.Fatalf("rejected batches left %d fresh projects behind (%v)", fresh, err)
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
		_, err := store.CommitAdoptionBatch(CommitAdoptionBatchInput{
			OperationID: operationID, RequestHash: operationID, PlanDigest: "sha256:" + operationID,
			Projects: []AdoptedProject{{
				Prefix: prefix, DisplayName: display,
				Publications: []AdoptedPublication{{
					Path: publicationPath, DisplayName: pubDisplay, EntryPoint: publicationPath + "/index.html",
					RoutingMode: "directory_index", ContentChangedAt: "2026-01-01T00:00:00Z",
					Objects: []AdoptedObject{{Key: publicationPath + "/index.html", RelativePath: "index.html", Size: 5, SHA256: fakeSHA("ee")}},
				}},
			}},
		})
		if err != nil {
			t.Fatalf("CommitAdoptionBatch(%s) error = %v", operationID, err)
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
	if len(inventory.Projects) != 3 {
		t.Fatalf("inventory has %d projects, want 3: %+v", len(inventory.Projects), inventory)
	}
	wantOrder := []string{"alpha project", "Beta archive", "Zeta notes"}
	for i, project := range inventory.Projects {
		if project.DisplayName != wantOrder[i] {
			t.Fatalf("project %d = %q, want %q", i, project.DisplayName, wantOrder[i])
		}
	}
	if inventory.Projects[0].Prefix != "alpha" || len(inventory.Projects[0].Publications) != 0 {
		t.Fatalf("empty project = %+v", inventory.Projects[0])
	}
	publications := inventory.Projects[2].Publications
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
