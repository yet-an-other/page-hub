package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/observe"
	"github.com/yet-an-other/page-hub/internal/server"
	"github.com/yet-an-other/page-hub/internal/storage"
)

type fakeRefresher struct {
	mu        sync.Mutex
	refreshes int
	running   bool
	last      observe.Outcome

	outcome observe.Outcome
	block   chan struct{}
}

func (f *fakeRefresher) Refresh(ctx context.Context) observe.Outcome {
	f.mu.Lock()
	f.refreshes++
	f.running = true
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	if f.outcome != "" {
		f.last = f.outcome
		return f.outcome
	}
	f.last = observe.OutcomeSucceeded
	return observe.OutcomeSucceeded
}

func (f *fakeRefresher) State() (bool, observe.Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, f.last
}

var _ server.Refresher = (*fakeRefresher)(nil)

func getInventory(t *testing.T, handler http.Handler) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/inventory", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	handler.ServeHTTP(recorder, request)
	var payload map[string]any
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode inventory response: %v", err)
		}
	}
	return recorder.Code, payload
}

func TestInventoryIdentifiesRunningRefreshAndStaleObservation(t *testing.T) {
	refresher := &fakeRefresher{running: true, last: observe.OutcomeSucceeded}
	// An observation older than five minutes is stale.
	observation := &catalog.BucketObservation{
		ObservedAt:   time.Now().Add(-10 * time.Minute),
		MutationLock: catalog.LockNone,
		Usage:        catalog.BucketUsage{TotalBytes: 150, AcceptedBytes: 150},
	}
	inventory := &fakeInventory{inventoryValue: catalog.Inventory{
		Projects: []catalog.InventoryProject{{
			ID: "project-id", Prefix: "notes", DisplayName: "Notes",
		}},
		Observation: observation,
	}}
	application, err := server.New(managerConfig(), &fakeChecker{}, inventory, refresher, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	code, payload := getInventory(t, application.Handler())
	if code != http.StatusOK {
		t.Fatalf("inventory status = %d, want 200", code)
	}
	refresh := payload["refresh"].(map[string]any)
	if refresh["running"] != true || refresh["lastOutcome"] != string(observe.OutcomeSucceeded) {
		t.Fatalf("refresh = %+v", refresh)
	}
	bucket := payload["observation"].(map[string]any)
	if bucket["stale"] != true {
		t.Fatalf("stale observation not marked: %+v", bucket)
	}
	if bucket["usage"].(map[string]any)["quotaBytes"].(float64) != float64(1<<30) {
		t.Fatalf("usage = %+v", bucket["usage"])
	}
}

func TestInventoryMarksFreshObservationNotStale(t *testing.T) {
	inventory := &fakeInventory{inventoryValue: catalog.Inventory{
		Observation: &catalog.BucketObservation{
			ObservedAt:   time.Now().Add(-time.Minute),
			MutationLock: catalog.LockNone,
		},
	}}
	application, err := server.New(managerConfig(), &fakeChecker{}, inventory, &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, payload := getInventory(t, application.Handler())
	if payload["observation"].(map[string]any)["stale"] != false {
		t.Fatalf("fresh observation marked stale: %+v", payload["observation"])
	}
	if payload["refresh"].(map[string]any)["running"] != false {
		t.Fatalf("refresh = %+v", payload["refresh"])
	}
}

func TestInventoryWithoutRefresherReportsNeverRefreshed(t *testing.T) {
	inventory := &fakeInventory{}
	application, err := server.New(managerConfig(), &fakeChecker{}, inventory, nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, payload := getInventory(t, application.Handler())
	refresh := payload["refresh"].(map[string]any)
	if refresh["running"] != false || refresh["lastOutcome"] != string(observe.OutcomeNever) {
		t.Fatalf("refresh = %+v, want never refreshed", refresh)
	}
}

func TestRefreshRequiresAuthentication(t *testing.T) {
	refresher := &fakeRefresher{}
	application, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, refresher, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/_page-hub/api/v1/refresh", nil)
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated refresh status = %d, want 401", recorder.Code)
	}
	if refresher.refreshes != 0 {
		t.Fatalf("refreshes = %d, want none", refresher.refreshes)
	}
}

func TestRefreshRunsObservationAndReportsState(t *testing.T) {
	refresher := &fakeRefresher{}
	application, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, refresher, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/_page-hub/api/v1/refresh", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", recorder.Code)
	}
	if refresher.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refresher.refreshes)
	}
	var payload struct {
		Running     bool   `json:"running"`
		LastOutcome string `json:"lastOutcome"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if payload.Running || payload.LastOutcome != string(observe.OutcomeSucceeded) {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRefreshRejectsOtherMethods(t *testing.T) {
	application, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, &fakeRefresher{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/api/v1/refresh", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET refresh status = %d, want 405", recorder.Code)
	}
}

func TestRefreshWithoutRefresherIsUnavailable(t *testing.T) {
	application, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/_page-hub/api/v1/refresh", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("refresh status = %d, want 503", recorder.Code)
	}
}

func TestRefreshResponseSurvivesServerWriteTimeout(t *testing.T) {
	// A scan slower than the server's WriteTimeout must still answer: the
	// refresh route lifts the write deadline for its own response.
	refresher := &fakeRefresher{block: make(chan struct{})}
	application, err := server.New(managerConfig(), &fakeChecker{}, &fakeInventory{}, refresher, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	backend := httptest.NewUnstartedServer(application.Handler())
	backend.Config.WriteTimeout = 50 * time.Millisecond
	backend.Start()
	defer backend.Close()

	type result struct {
		status int
		err    error
	}
	results := make(chan result, 1)
	go func() {
		request, err := http.NewRequest(http.MethodPost, backend.URL+"/_page-hub/api/v1/refresh", nil)
		if err != nil {
			results <- result{err: err}
			return
		}
		request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			results <- result{err: err}
			return
		}
		results <- result{status: response.StatusCode}
		_ = response.Body.Close()
	}()

	// Hold the scan well past the write deadline before letting it finish.
	waitUntil(t, time.Second, func() bool { return refresher.refreshes > 0 })
	time.Sleep(150 * time.Millisecond)
	close(refresher.block)

	select {
	case got := <-results:
		if got.err != nil {
			t.Fatalf("refresh request failed: %v", got.err)
		}
		if got.status != http.StatusOK {
			t.Fatalf("refresh status = %d, want 200", got.status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh response never arrived")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition was not met in time")
}

func TestReadinessReportsDegradedWhenCatalogUnavailable(t *testing.T) {
	application, err := server.New(managerConfig(),
		&fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}},
		&fakeInventory{err: errors.New("catalog disk failure detail")}, nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/readyz", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", recorder.Code)
	}
	var payload struct {
		Status  string `json:"status"`
		Storage string `json:"storage"`
		Catalog string `json:"catalog"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if payload.Status != "degraded" || payload.Catalog != "unavailable" || payload.Storage != "reachable" {
		t.Fatalf("payload = %+v", payload)
	}
	// The failure detail must not leak.
	if strings.Contains(recorder.Body.String(), "catalog disk failure detail") {
		t.Fatal("readiness response leaked catalog failure detail")
	}
}

func TestReadinessReportsDegradedWhenStorageUnreachable(t *testing.T) {
	application, err := server.New(managerConfig(),
		&fakeChecker{result: storage.CheckResult{Status: storage.StatusUnavailable}},
		&fakeInventory{}, nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/readyz", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"storage":"unavailable"`) || !strings.Contains(recorder.Body.String(), `"catalog":"available"`) {
		t.Fatalf("payload = %s", recorder.Body.String())
	}
}

func TestReadinessReportsReadyWithHealthyCatalogAndStorage(t *testing.T) {
	application, err := server.New(managerConfig(),
		&fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}},
		&fakeInventory{}, nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_page-hub/readyz", nil)
	request.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"ready"`) {
		t.Fatalf("payload = %s", recorder.Body.String())
	}
}
