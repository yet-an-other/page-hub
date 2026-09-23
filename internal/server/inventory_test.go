package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/server"
)

type fakeInventory struct {
	projects []catalog.InventoryProject
	err      error
}

func (f *fakeInventory) Inventory(ctx context.Context) ([]catalog.InventoryProject, error) {
	return f.projects, f.err
}

var _ server.InventoryReader = (*fakeInventory)(nil)

func TestInventoryRequiresAuthentication(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil)
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inventory status = %d, want 401", recorder.Code)
	}
}

func TestInventoryReturnsCatalogedProjects(t *testing.T) {
	inventory := &fakeInventory{projects: []catalog.InventoryProject{{
		ID:          "project-id",
		Prefix:      "notes",
		DisplayName: "Notes",
		Publications: []catalog.InventoryPublication{{
			ID:               "publication-id",
			Path:             "notes/2026-report",
			DisplayName:      "2026 Report",
			EntryPoint:       "notes/2026-report/index.html",
			RoutingMode:      "directory_index",
			Size:             150,
			ContentChangedAt: "2026-01-02T03:04:05Z",
		}},
	}}}
	app, err := server.New(managerConfig(), &fakeChecker{}, inventory, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("inventory status = %d, want 200", recorder.Code)
	}
	var payload struct {
		Projects []catalog.InventoryProject `json:"projects"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory response: %v", err)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].Prefix != "notes" || len(payload.Projects[0].Publications) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Projects[0].Publications[0].Size != 150 {
		t.Fatalf("publication size = %d", payload.Projects[0].Publications[0].Size)
	}
}

func TestInventoryFailureDoesNotLeakCatalogDetails(t *testing.T) {
	inventory := &fakeInventory{err: errors.New("secret-path /var/lib/catalog locked: private detail")}
	app, err := server.New(managerConfig(), &fakeChecker{}, inventory, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("inventory failure status = %d, want 500", recorder.Code)
	}
	if body := recorder.Body.String(); body == "" || strings.Contains(body, "secret-path") || strings.Contains(body, "private detail") {
		t.Fatalf("inventory failure leaked internals: %s", body)
	}
}
