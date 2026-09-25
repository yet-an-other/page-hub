package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/server"
)

// previewInventory builds a fake inventory holding one Notes Project with the
// given publications and bucket observation.
func previewInventory(t *testing.T, observation *catalog.BucketObservation, publications ...catalog.InventoryPublication) *fakeInventory {
	t.Helper()
	return &fakeInventory{inventoryValue: catalog.Inventory{
		Projects: []catalog.InventoryProject{{
			ID:           "project-id",
			Prefix:       "notes",
			DisplayName:  "Notes",
			Publications: publications,
		}},
		Observation: observation,
	}}
}

func inSyncReport(t *testing.T) *fakeInventory {
	t.Helper()
	return previewInventory(t, &catalog.BucketObservation{
		ObservedAt: time.Date(2026, 1, 3, 8, 0, 0, 0, time.UTC),
		Usage:      catalog.BucketUsage{QuotaBytes: 1 << 30, TotalBytes: 150, AcceptedBytes: 150},
	}, catalog.InventoryPublication{
		ID:               "publication-id",
		Path:             "notes/2026-report",
		DisplayName:      "2026 Report",
		EntryPoint:       "notes/2026-report/index.html",
		RoutingMode:      "directory_index",
		Size:             150,
		ContentChangedAt: "2026-01-02T03:04:05Z",
		Observation:      &catalog.PublicationObservation{State: catalog.StateInSync},
	})
}

func authenticatedPreviewRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	return request
}

// exactFileReport inventories an in-sync Publication published the way the
// web-share skill publishes one file: the object keeps its filename under the
// Project prefix, so the public URL includes the entry point, not just the
// Publication path.
func exactFileReport(t *testing.T) *fakeInventory {
	t.Helper()
	return previewInventory(t, &catalog.BucketObservation{
		ObservedAt: time.Date(2026, 1, 3, 8, 0, 0, 0, time.UTC),
		Usage:      catalog.BucketUsage{QuotaBytes: 1 << 30, TotalBytes: 50, AcceptedBytes: 50},
	}, catalog.InventoryPublication{
		ID:               "snapshot-id",
		Path:             "archive/snapshot",
		DisplayName:      "Snapshot",
		EntryPoint:       "archive/snapshot/Report.HTML",
		RoutingMode:      "exact_file",
		Size:             50,
		ContentChangedAt: "2025-11-20T08:00:00Z",
		Observation:      &catalog.PublicationObservation{State: catalog.StateInSync},
	})
}

func TestPreviewRedirectsInSyncPublicationToCanonicalURL(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, inSyncReport(t), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, authenticatedPreviewRequest("/_page-hub/preview/notes/2026-report"))

	if recorder.Code != http.StatusFound {
		t.Fatalf("preview status = %d, want 302", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "https://share.bdgn.me/notes/2026-report" {
		t.Fatalf("Location = %q, want the canonical public URL", location)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preview redirect must not be cached")
	}
}

func TestPreviewRedirectsExactFilePublicationToItsEntryPointURL(t *testing.T) {
	// An HTML page published as <project>/<filename> must redirect to the
	// entry point's URL: the page readers actually request.
	app, err := server.New(managerConfig(), &fakeChecker{}, exactFileReport(t), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, authenticatedPreviewRequest("/_page-hub/preview/archive/snapshot"))

	if recorder.Code != http.StatusFound {
		t.Fatalf("preview status = %d, want 302", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "https://share.bdgn.me/archive/snapshot/Report.HTML" {
		t.Fatalf("Location = %q, want the entry point's public URL", location)
	}
}

func TestPreviewWarnsInsteadOfRedirectingWhenNotInSync(t *testing.T) {
	drifted := int64(999)
	missing := catalog.InventoryPublication{
		ID:               "missing-id",
		Path:             "notes/gone",
		DisplayName:      "Gone",
		EntryPoint:       "notes/gone/index.html",
		RoutingMode:      "directory_index",
		Size:             150,
		ContentChangedAt: "2026-01-02T03:04:05Z",
		Observation:      &catalog.PublicationObservation{State: catalog.StateMissing},
	}
	driftedReport := catalog.InventoryPublication{
		ID:               "drifted-id",
		Path:             "notes/2026-report",
		DisplayName:      "2026 Report",
		EntryPoint:       "notes/2026-report/index.html",
		RoutingMode:      "directory_index",
		Size:             150,
		ContentChangedAt: "2026-01-02T03:04:05Z",
		Observation: &catalog.PublicationObservation{
			State:        catalog.StateDrifted,
			ObservedSize: &drifted,
			StatusDetail: "1 of 2 object(s) differ",
		},
	}
	observation := &catalog.BucketObservation{
		ObservedAt: time.Date(2026, 1, 3, 8, 0, 0, 0, time.UTC),
		Stale:      true,
		Usage:      catalog.BucketUsage{QuotaBytes: 1 << 30, TotalBytes: 999, AcceptedBytes: 150, UnclaimedBytes: 849},
	}
	app, err := server.New(managerConfig(), &fakeChecker{}, previewInventory(t, observation, driftedReport, missing), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, test := range []struct {
		name         string
		target       string
		wantFragment string
	}{
		{name: "drifted", target: "/_page-hub/preview/notes/2026-report", wantFragment: "drifted"},
		{name: "drifted status detail", target: "/_page-hub/preview/notes/2026-report", wantFragment: "1 of 2 object(s) differ"},
		{name: "missing", target: "/_page-hub/preview/notes/gone", wantFragment: "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, authenticatedPreviewRequest(test.target))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("preview status = %d, want 409", recorder.Code)
			}
			if location := recorder.Header().Get("Location"); location != "" {
				t.Fatalf("Location = %q, want no redirect", location)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, test.wantFragment) {
				t.Fatalf("preview warning page %q does not contain %q", body, test.wantFragment)
			}
			if !strings.Contains(body, "2026-01-03 08:00") {
				t.Fatalf("preview warning page %q does not contain the observation time", body)
			}
			if !strings.Contains(body, "stale") {
				t.Fatalf("preview warning page %q does not report staleness", body)
			}
		})
	}
}

func TestPreviewWarnsBeforeTheFirstObservation(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, previewInventory(t, nil, catalog.InventoryPublication{
		ID:               "publication-id",
		Path:             "notes/2026-report",
		DisplayName:      "2026 Report",
		EntryPoint:       "notes/2026-report/index.html",
		RoutingMode:      "directory_index",
		Size:             150,
		ContentChangedAt: "2026-01-02T03:04:05Z",
	}), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, authenticatedPreviewRequest("/_page-hub/preview/notes/2026-report"))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("preview status = %d, want 409", recorder.Code)
	}
	if recorder.Header().Get("Location") != "" {
		t.Fatal("an unobserved publication must never redirect")
	}
}

func TestPreviewUnknownPublicationWarnsWithoutRedirect(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, inSyncReport(t), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, authenticatedPreviewRequest("/_page-hub/preview/notes/absent"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("preview status = %d, want 404", recorder.Code)
	}
	if recorder.Header().Get("Location") != "" {
		t.Fatal("an unknown publication must never redirect")
	}
}

func TestPreviewRejectsTraversalAndEmptyPaths(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, inSyncReport(t), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, target := range []string{
		"/_page-hub/preview/",
		"/_page-hub/preview/..",
		"/_page-hub/preview/notes/../notes/2026-report",
	} {
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, authenticatedPreviewRequest(target))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("preview of %q status = %d, want a 404 warning page", target, recorder.Code)
		}
		if recorder.Header().Get("Location") != "" {
			t.Fatalf("preview of %q must never redirect", target)
		}
	}
}

func TestPreviewRequiresAuthentication(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, inSyncReport(t), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/preview/notes/2026-report", nil)
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated preview status = %d, want 401", recorder.Code)
	}
}

func TestPreviewRejectsNonGetMethods(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{}, inSyncReport(t), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/_page-hub/preview/notes/2026-report", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST preview status = %d, want 405", recorder.Code)
	}
}
