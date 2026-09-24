package catalog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/yet-an-other/page-hub/internal/catalog"
)

// TestInventoryRendersEmptyProjects pins that a Project without Publications
// appears in the inventory with an empty publications array: moves and
// deletions can empty a Project later, and the manager must still show it.
// Adoption cannot create this state, so the test inserts one directly.
func TestInventoryRendersEmptyProjects(t *testing.T) {
	catalogPath := filepath.Join(t.TempDir(), "empty-project-catalog.db")
	if _, _, err := catalog.Migrate(catalogPath); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()

	db, err := sql.Open("sqlite", catalogPath)
	if err != nil {
		t.Fatalf("open raw catalog: %v", err)
	}
	defer db.Close()
	seededAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO projects (id, prefix, display_name, description, entity_revision, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		catalog.NewID(), "empty", "Empty Shelf", "An empty project", "test-revision", seededAt, seededAt,
	); err != nil {
		t.Fatalf("insert empty project: %v", err)
	}

	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	var empty *catalog.InventoryProject
	for i := range inventory.Projects {
		if inventory.Projects[i].Prefix == "empty" {
			empty = &inventory.Projects[i]
		}
	}
	if empty == nil {
		t.Fatal("Inventory() omitted the empty Project")
	}
	if len(empty.Publications) != 0 {
		t.Fatalf("empty Project publications = %+v, want none", empty.Publications)
	}
	// The JSON contract promises an array, never null.
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty project: %v", err)
	}
	var decoded struct {
		Publications []json.RawMessage `json:"publications"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode empty project: %v", err)
	}
	if decoded.Publications == nil {
		t.Fatalf("empty Project marshaled as %s, want a publications array", encoded)
	}
}
