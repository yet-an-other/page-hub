package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestS3CheckerUsesReadOnlyHeadBucketRequest(t *testing.T) {
	var method, path string
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer storageServer.Close()

	checker := NewS3Checker(Config{
		Endpoint:        storageServer.URL,
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	result := checker.Check(context.Background())
	if result.Status != StatusReachable {
		t.Fatalf("status = %q, want %q", result.Status, StatusReachable)
	}
	if method != http.MethodHead {
		t.Fatalf("method = %q, want HEAD", method)
	}
	if path != "/page-hub" {
		t.Fatalf("path = %q, want /page-hub", path)
	}
}

func TestS3CheckerSignsConfiguredCredentials(t *testing.T) {
	var authorization string
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.Header.Get("X-Amz-Date") == "" {
			t.Error("signed request is missing X-Amz-Date")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer storageServer.Close()

	checker := NewS3Checker(Config{
		Endpoint:        storageServer.URL,
		Region:          "us-test-1",
		Bucket:          "page-hub",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
	})
	result := checker.Check(context.Background())
	if result.Status != StatusReachable {
		t.Fatalf("status = %q, want %q", result.Status, StatusReachable)
	}
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=access-key/") {
		t.Fatalf("authorization = %q, want a SigV4 credential", authorization)
	}
}

func TestS3CheckerDoesNotExposeStorageFailureDetails(t *testing.T) {
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("access key=do-not-return-this"))
	}))
	defer storageServer.Close()

	checker := NewS3Checker(Config{
		Endpoint:        storageServer.URL,
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	result := checker.Check(context.Background())
	if result.Status != StatusMisconfigured {
		t.Fatalf("status = %q, want %q", result.Status, StatusMisconfigured)
	}
}

func TestS3CheckerClassifiesUnavailableEndpoint(t *testing.T) {
	checker := NewS3Checker(Config{
		Endpoint:        "http://127.0.0.1:1",
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	result := checker.Check(context.Background())
	if result.Status != StatusUnavailable {
		t.Fatalf("status = %q, want %q", result.Status, StatusUnavailable)
	}
}

func TestS3CheckerClassifiesInvalidConfiguration(t *testing.T) {
	checker := NewS3Checker(Config{Endpoint: "not-an-url", Bucket: "page-hub"})
	result := checker.Check(context.Background())
	if result.Status != StatusMisconfigured {
		t.Fatalf("status = %q, want %q", result.Status, StatusMisconfigured)
	}
}
