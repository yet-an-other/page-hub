package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yet-an-other/page-hub/internal/config"
	"github.com/yet-an-other/page-hub/internal/server"
	"github.com/yet-an-other/page-hub/internal/storage"
)

type fakeChecker struct {
	result storage.CheckResult
	calls  atomic.Int32
}

func (f *fakeChecker) Check(context.Context) storage.CheckResult {
	f.calls.Add(1)
	return f.result
}

func managerConfig() config.Config {
	return config.Config{
		ListenAddr:          "127.0.0.1:8080",
		AuthAssertionHeader: "X-Page-Hub-Assertion",
		AuthAssertionValue:  "test-only-assertion",
		Version:             "test-version",
		CatalogPath:         "/tmp/test-catalog.db",
	}
}

func TestManagerRejectsMissingInvalidAndSpoofedAssertions(t *testing.T) {
	checker := &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}
	var publicCalls atomic.Int32
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		publicCalls.Add(1)
		w.WriteHeader(http.StatusTeapot)
	})

	app, err := server.New(managerConfig(), checker, nil, public)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	for _, test := range []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "invalid", header: "not-the-assertion"},
		{name: "client-spoofed", header: "client-value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://page-hub.test/", nil)
			if test.header != "" {
				req.Header.Set("X-Page-Hub-Assertion", test.header)
			}
			res := httptest.NewRecorder()
			app.Handler().ServeHTTP(res, req)

			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
			}
			if publicCalls.Load() != 0 {
				t.Fatal("unauthorized manager request reached public reader")
			}
			if strings.Contains(res.Body.String(), "test-only-assertion") {
				t.Fatal("authentication assertion leaked in response")
			}
		})
	}
}

func TestUnauthenticatedManagerDocumentCanRedirectToGatewayLogin(t *testing.T) {
	cfg := managerConfig()
	cfg.AuthLoginURL = "https://login.example.invalid/start"
	app, err := server.New(cfg, &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}, nil, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://page-hub.test/", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusFound)
	}
	if got := res.Header().Get("Location"); got != cfg.AuthLoginURL {
		t.Fatalf("location = %q, want %q", got, cfg.AuthLoginURL)
	}
}

func TestAuthenticatedManagerServesShellAndStatus(t *testing.T) {
	checker := &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}
	app, err := server.New(managerConfig(), checker, nil, nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	shellRequest := httptest.NewRequest(http.MethodGet, "http://page-hub.test/", nil)
	shellRequest.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	shellResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(shellResponse, shellRequest)
	if shellResponse.Code != http.StatusOK {
		t.Fatalf("shell status = %d, want %d", shellResponse.Code, http.StatusOK)
	}
	if !strings.Contains(shellResponse.Body.String(), "Page Hub") {
		t.Fatal("manager shell did not load")
	}
	if shellResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("shell cache policy = %q, want no-store", shellResponse.Header().Get("Cache-Control"))
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "http://page-hub.test/_page-hub/api/v1/status", nil)
	statusRequest.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	statusResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, want %d", statusResponse.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	storageBody, ok := body["storage"].(map[string]any)
	if !ok || storageBody["status"] != string(storage.StatusReachable) {
		t.Fatalf("unexpected status response: %#v", body)
	}
	if strings.Contains(statusResponse.Body.String(), "test-only-assertion") {
		t.Fatal("status response leaked authentication configuration")
	}
}

func TestReservedRouteFailureCannotFallThroughToPublicReader(t *testing.T) {
	checker := &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}
	var publicCalls atomic.Int32
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		publicCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("public publication"))
	})
	app, err := server.New(managerConfig(), checker, nil, public)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://page-hub.test/_page-hub/does-not-exist", nil)
	req.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}
	if strings.Contains(res.Body.String(), "public publication") {
		t.Fatal("reserved route returned public publication content")
	}
	if publicCalls.Load() != 0 {
		t.Fatal("reserved route reached public reader")
	}
}

func TestPublicReaderDoesNotReceiveManagerCredentials(t *testing.T) {
	checker := &fakeChecker{result: storage.CheckResult{Status: storage.StatusReachable}}
	public := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Page-Hub-Assertion"); got != "" {
			t.Errorf("assertion reached public reader: %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("authorization reached public reader: %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("cookie reached public reader: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})
	app, err := server.New(managerConfig(), checker, nil, public)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://page-hub.test/public/path", nil)
	req.Header.Set("X-Page-Hub-Assertion", "test-only-assertion")
	req.Header.Set("Authorization", "Bearer manager-secret")
	req.Header.Set("Cookie", "session=private")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
}
