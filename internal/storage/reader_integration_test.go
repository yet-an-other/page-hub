package storage_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

func testConfig(endpoint string) storage.Config {
	return storage.Config{
		Endpoint:        endpoint,
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}
}

func objectWith(content []byte, contentType string, metadata map[string]string) s3test.Object {
	return s3test.Object{
		Content:      content,
		ContentType:  contentType,
		UserMetadata: metadata,
		LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ETag:         `"opaque-etag"`,
	}
}

func TestS3ReaderListsCompleteBucketWithPagination(t *testing.T) {
	objects := map[string]s3test.Object{}
	for _, key := range []string{"site/a.html", "site/b.html", "site/c.html", "site/d.html", "site/e.html"} {
		objects[key] = objectWith([]byte("body of "+key), "text/html", nil)
	}
	bucket := s3test.NewServer("page-hub", objects)
	defer bucket.Close()

	reader := storage.NewS3ReaderWithHTTPClient(testConfig(bucket.URL()), bucket.HTTPClient())
	listing, err := reader.ListObjects(context.Background())
	if err != nil {
		t.Fatalf("ListObjects() error = %v", err)
	}
	if len(listing) != 5 {
		t.Fatalf("listing has %d objects, want 5", len(listing))
	}
	seen := map[string]storage.ObjectListing{}
	for _, entry := range listing {
		seen[entry.Key] = entry
	}
	for _, key := range []string{"site/a.html", "site/b.html", "site/c.html", "site/d.html", "site/e.html"} {
		entry, ok := seen[key]
		if !ok {
			t.Fatalf("listing is missing %q", key)
		}
		if entry.Size != int64(len("body of "+key)) || entry.ETag != `"opaque-etag"` {
			t.Fatalf("entry %q = %+v", key, entry)
		}
		if !entry.LastModified.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
			t.Fatalf("entry %q modified = %v", key, entry.LastModified)
		}
	}

	// The listing walked every page: continuation tokens were followed.
	var sawContinuation bool
	for _, request := range bucket.Requests() {
		if request.Method != http.MethodGet {
			t.Fatalf("listing issued %s %s, only GET is allowed", request.Method, request.Path)
		}
		if strings.Contains(request.Query, "continuation-token") {
			sawContinuation = true
		}
	}
	if !sawContinuation {
		t.Fatal("expected pagination continuation tokens")
	}
}

func TestS3ReaderReadsObjectWithMetadataAndDigest(t *testing.T) {
	content := []byte("<html>exact body</html>")
	sum := sha256.Sum256(content)
	bucket := s3test.NewServer("page-hub", map[string]s3test.Object{
		"notes/2026-report/index.HTML": {
			Content:         content,
			ContentType:     "text/html; charset=utf-8",
			ContentEncoding: "gzip",
			CacheControl:    "public, max-age=3600",
			UserMetadata:    map[string]string{"owner": "operator", "source": "legacy"},
			LastModified:    time.Date(2025, 12, 31, 23, 59, 58, 0, time.UTC),
			ETag:            `"etag-123"`,
		},
	})
	defer bucket.Close()

	reader := storage.NewS3ReaderWithHTTPClient(testConfig(bucket.URL()), bucket.HTTPClient())
	object, err := reader.ReadObject(context.Background(), "notes/2026-report/index.HTML")
	if err != nil {
		t.Fatalf("ReadObject() error = %v", err)
	}
	if string(object.Body) != string(content) {
		t.Fatalf("body = %q", object.Body)
	}
	if object.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", object.Size, len(content))
	}
	if object.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %q, want %q", object.SHA256, hex.EncodeToString(sum[:]))
	}
	if object.ETag != `"etag-123"` || object.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("metadata = %+v", object)
	}
	if object.ContentEncoding != "gzip" || object.CacheControl != "public, max-age=3600" {
		t.Fatalf("metadata = %+v", object)
	}
	if object.UserMetadata["owner"] != "operator" || object.UserMetadata["source"] != "legacy" {
		t.Fatalf("user metadata = %v", object.UserMetadata)
	}
	if !object.LastModified.Equal(time.Date(2025, 12, 31, 23, 59, 58, 0, time.UTC)) {
		t.Fatalf("last modified = %v", object.LastModified)
	}
}

func TestS3ReaderClassifiesMissingObject(t *testing.T) {
	bucket := s3test.NewServer("page-hub", nil)
	defer bucket.Close()

	reader := storage.NewS3ReaderWithHTTPClient(testConfig(bucket.URL()), bucket.HTTPClient())
	_, err := reader.ReadObject(context.Background(), "absent.html")
	if !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("error = %v, want ErrObjectNotFound", err)
	}
}

func TestS3ReaderClassifiesUnavailableStorage(t *testing.T) {
	bucket := s3test.NewServer("page-hub", nil)
	endpoint := bucket.URL()
	bucket.Close()

	reader := storage.NewS3Reader(testConfig(endpoint))
	if _, err := reader.ListObjects(context.Background()); !errors.Is(err, storage.ErrStorageUnavailable) {
		t.Fatalf("listing error = %v, want ErrStorageUnavailable", err)
	}
	if _, err := reader.ReadObject(context.Background(), "any.html"); !errors.Is(err, storage.ErrStorageUnavailable) {
		t.Fatalf("read error = %v, want ErrStorageUnavailable", err)
	}
}

func TestS3ReaderSignsEveryRequest(t *testing.T) {
	bucket := s3test.NewServer("page-hub", map[string]s3test.Object{
		"one.html": objectWith([]byte("1"), "text/html", nil),
	})
	defer bucket.Close()

	reader := storage.NewS3ReaderWithHTTPClient(testConfig(bucket.URL()), bucket.HTTPClient())
	if _, err := reader.ListObjects(context.Background()); err != nil {
		t.Fatalf("ListObjects() error = %v", err)
	}
	if _, err := reader.ReadObject(context.Background(), "one.html"); err != nil {
		t.Fatalf("ReadObject() error = %v", err)
	}
	for _, request := range bucket.Requests() {
		if !request.Signed {
			t.Fatalf("request %s %s was not signed with the configured credentials", request.Method, request.Path)
		}
	}
}
