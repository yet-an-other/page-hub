package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/server"
)

type fakeInventory struct {
	inventoryValue catalog.Inventory
	err            error
}

func (f *fakeInventory) Inventory(ctx context.Context) (catalog.Inventory, error) {
	return f.inventoryValue, f.err
}

var _ server.InventoryReader = (*fakeInventory)(nil)

func TestInventoryRequiresAuthentication(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, &fakeRefresher{}, nil)
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

func TestInventoryReturnsCatalogedProjectsWithObservation(t *testing.T) {
	observedSize := int64(150)
	inventory := &fakeInventory{inventoryValue: catalog.Inventory{
		Projects: []catalog.InventoryProject{{
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
				Observation: &catalog.PublicationObservation{
					State:        catalog.StateDrifted,
					ObservedSize: &observedSize,
					StatusDetail: "1 of 2 object(s) differ",
				},
			}},
		}},
		Observation: &catalog.BucketObservation{
			ObservedAt:   time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
			MutationLock: catalog.LockGlobal,
			Usage:        catalog.BucketUsage{TotalBytes: 400, AcceptedBytes: 150, UnclaimedBytes: 250},
		},
	}}
	app, err := server.New(managerConfig(), &fakeChecker{}, inventory, &fakeRefresher{}, nil)
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
		Projects    []catalog.InventoryProject `json:"projects"`
		Observation *struct {
			ObservedAt   string `json:"observedAt"`
			MutationLock string `json:"mutationLock"`
			Usage        struct {
				QuotaBytes     int64 `json:"quotaBytes"`
				TotalBytes     int64 `json:"totalBytes"`
				AcceptedBytes  int64 `json:"acceptedBytes"`
				UnclaimedBytes int64 `json:"unclaimedBytes"`
			} `json:"usage"`
		} `json:"observation"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory response: %v", err)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].Prefix != "notes" || len(payload.Projects[0].Publications) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	publication := payload.Projects[0].Publications[0]
	if publication.Size != 150 {
		t.Fatalf("publication size = %d", publication.Size)
	}
	if publication.Observation == nil || publication.Observation.State != catalog.StateDrifted || publication.Observation.StatusDetail != "1 of 2 object(s) differ" {
		t.Fatalf("publication observation = %+v", publication.Observation)
	}
	if payload.Observation == nil {
		t.Fatal("bucket observation missing from payload")
	}
	if payload.Observation.MutationLock != catalog.LockGlobal || payload.Observation.Usage.TotalBytes != 400 {
		t.Fatalf("bucket observation = %+v", payload.Observation)
	}
	// The quota comes from the deployment configuration, not the catalog.
	if payload.Observation.Usage.QuotaBytes != int64(1<<30) {
		t.Fatalf("quota bytes = %d, want %d", payload.Observation.Usage.QuotaBytes, int64(1<<30))
	}
	// Canonical public URLs come from the configured public base URL.
	if publication.CanonicalURL != "https://share.bdgn.me/notes/2026-report" {
		t.Fatalf("canonical URL = %q", publication.CanonicalURL)
	}
}

func TestInventoryCanonicalURLUsesTheEntryPointForExactFilePublications(t *testing.T) {
	inventory := &fakeInventory{inventoryValue: catalog.Inventory{
		Projects: []catalog.InventoryProject{{
			ID:     "project-id",
			Prefix: "archive",
			Publications: []catalog.InventoryPublication{{
				ID:          "publication-id",
				Path:        "archive/snapshot",
				DisplayName: "Snapshot",
				EntryPoint:  "archive/snapshot/Report.HTML",
				RoutingMode: "exact_file",
			}},
		}},
	}}
	app, err := server.New(managerConfig(), &fakeChecker{}, inventory, &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	app.Handler().ServeHTTP(recorder, request)

	var payload struct {
		Projects []catalog.InventoryProject `json:"projects"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory response: %v", err)
	}
	if got := payload.Projects[0].Publications[0].CanonicalURL; got != "https://share.bdgn.me/archive/snapshot/Report.HTML" {
		t.Fatalf("canonical URL = %q, want the entry point's public URL", got)
	}
}

func TestInventoryWithoutObservationOmitsIt(t *testing.T) {
	inventory := &fakeInventory{inventoryValue: catalog.Inventory{
		Projects: []catalog.InventoryProject{{
			ID: "project-id", Prefix: "notes", DisplayName: "Notes",
		}},
	}}
	app, err := server.New(managerConfig(), &fakeChecker{}, inventory, &fakeRefresher{}, nil)
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
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory response: %v", err)
	}
	if payload["observation"] != nil {
		t.Fatalf("observation = %v, want null", payload["observation"])
	}
}

func TestInventoryFailureDoesNotLeakCatalogDetails(t *testing.T) {
	inventory := &fakeInventory{err: errors.New("secret-path /var/lib/catalog locked: private detail")}
	app, err := server.New(managerConfig(), &fakeChecker{}, inventory, &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("inventory failure status = %d, want 503", recorder.Code)
	}
	if body := recorder.Body.String(); body == "" || strings.Contains(body, "secret-path") || strings.Contains(body, "private detail") {
		t.Fatalf("inventory failure leaked internals: %s", body)
	}
}
