package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/observe"
	"github.com/yet-an-other/page-hub/internal/server"
	"github.com/yet-an-other/page-hub/internal/storage"
)

func authenticatedRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	return request
}

// hygieneInventory holds values that must never reach logs or health output:
// private descriptions, exact paths, and observation details.
func hygieneInventory() *fakeInventory {
	observedSize := int64(150)
	return &fakeInventory{inventoryValue: catalog.Inventory{
		Projects: []catalog.InventoryProject{{
			ID:          "project-id",
			Prefix:      "notes",
			DisplayName: "Notes",
			Description: "project-private-note-do-not-log",
			Publications: []catalog.InventoryPublication{{
				ID:               "publication-id",
				Path:             "notes/2026-report",
				DisplayName:      "2026 Report",
				Description:      "publication-private-note-do-not-log",
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
			ObservedAt: time.Date(2026, 1, 3, 8, 0, 0, 0, time.UTC),
			Usage:      catalog.BucketUsage{QuotaBytes: 1 << 30, TotalBytes: 999, AcceptedBytes: 150, UnclaimedBytes: 849},
		},
	}}
}

func TestRequestHandlingLogsNoSecrets(t *testing.T) {
	var logBuffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cfg := managerConfig()
	cfg.Storage = storage.Config{
		Endpoint:        "https://storage.example.invalid",
		Bucket:          "publications",
		AccessKeyID:     "secret-access-key-id",
		SecretAccessKey: "secret-access-key-value",
		SessionToken:    "secret-session-token",
	}
	failingInventory := &fakeInventory{err: errors.New("catalog: private-sqlite-failure-detail")}
	refresher := &fakeRefresher{outcome: observe.OutcomeFailed}
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>public-source-content-marker</html>"))
	})
	app, err := server.New(cfg, &fakeChecker{result: storage.CheckResult{Status: storage.StatusUnavailable}}, failingInventory, refresher, public)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	requests := []*http.Request{
		authenticatedRequest(http.MethodGet, "/"),
		authenticatedRequest(http.MethodGet, "/_page-hub/api/v1/inventory"),
		authenticatedRequest(http.MethodPost, "/_page-hub/api/v1/refresh"),
		authenticatedRequest(http.MethodGet, "/_page-hub/preview/notes/2026-report"),
		authenticatedRequest(http.MethodGet, "/_page-hub/healthz"),
		authenticatedRequest(http.MethodGet, "/_page-hub/readyz"),
		authenticatedRequest(http.MethodGet, "/_page-hub/does-not-exist"),
		httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil), // missing assertion
		httptest.NewRequest(http.MethodGet, "/", nil),
		authenticatedRequest(http.MethodGet, "/notes/2026-report"), // public route with manager headers
	}
	for _, request := range requests {
		app.Handler().ServeHTTP(httptest.NewRecorder(), request)
	}

	for _, secret := range []string{
		"test-only-assertion",
		"project-private-note-do-not-log",
		"publication-private-note-do-not-log",
		"secret-access-key-id",
		"secret-access-key-value",
		"secret-session-token",
		"private-sqlite-failure-detail",
		"public-source-content-marker",
	} {
		if bytes.Contains(logBuffer.Bytes(), []byte(secret)) {
			t.Fatalf("request handling logged the secret %q; logs:\n%s", secret, logBuffer.String())
		}
	}
}

func TestHealthAndReadinessRevealNoCatalogData(t *testing.T) {
	app, err := server.New(managerConfig(), &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}, hygieneInventory(), &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	healthRecorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(healthRecorder, authenticatedRequest(http.MethodGet, "/_page-hub/healthz"))
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", healthRecorder.Code)
	}
	var health map[string]any
	if err := json.Unmarshal(healthRecorder.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if len(health) != 2 || health["status"] != "ok" || health["version"] != "test-version" {
		t.Fatalf("healthz body = %v, want only status and version", health)
	}

	readyRecorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(readyRecorder, authenticatedRequest(http.MethodGet, "/_page-hub/readyz"))
	var ready map[string]any
	if err := json.Unmarshal(readyRecorder.Body.Bytes(), &ready); err != nil {
		t.Fatalf("decode readyz: %v", err)
	}
	if len(ready) != 3 || ready["status"] != "ready" || ready["storage"] != string(storage.StatusReachable) || ready["catalog"] != "available" {
		t.Fatalf("readyz body = %v, want only status, storage, and catalog", ready)
	}

	// Neither response may reveal catalog names, descriptions, or the
	// configured assertion.
	for _, body := range []string{healthRecorder.Body.String(), readyRecorder.Body.String()} {
		for _, secret := range []string{"Notes", "2026 Report", "notes/2026-report", "test-only-assertion", "project-private-note", "publication-private-note"} {
			if bytes.Contains([]byte(body), []byte(secret)) {
				t.Fatalf("health response leaked %q: %s", secret, body)
			}
		}
	}
}

func TestAuthenticationFailuresNeverExposePublicContent(t *testing.T) {
	var publicCalls atomic.Int32
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		publicCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("public-publication-content-marker"))
	})
	app, err := server.New(managerConfig(), &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}, hygieneInventory(), &fakeRefresher{}, public)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	for _, target := range []string{
		"/",
		"/_page-hub",
		"/_page-hub/api/v1/status",
		"/_page-hub/api/v1/inventory",
		"/_page-hub/api/v1/refresh",
		"/_page-hub/preview/notes/2026-report",
		"/_page-hub/healthz",
		"/_page-hub/readyz",
		"/_page-hub/does-not-exist",
	} {
		t.Run(target, func(t *testing.T) {
			method := http.MethodGet
			if target == "/_page-hub/api/v1/refresh" {
				method = http.MethodPost
			}
			// No assertion header: the manager must refuse the request on
			// its own and never reach the public reader.
			request := httptest.NewRequest(method, target, nil)
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			if bytes.Contains(recorder.Body.Bytes(), []byte("public-publication-content-marker")) {
				t.Fatalf("authentication failure returned public Publication content for %s", target)
			}
			if publicCalls.Load() != 0 {
				t.Fatalf("authentication failure for %s reached the public reader", target)
			}
		})
	}
}
