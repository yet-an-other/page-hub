package storage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

func compatReader(t *testing.T, bucket *s3test.Server) *storage.S3Reader {
	t.Helper()
	return storage.NewS3Reader(storage.Config{
		Endpoint:        bucket.URL(),
		Region:          "test-region",
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
}

func TestCheckCompatibilityListsDownloadsAndOnlyReads(t *testing.T) {
	objects := map[string]s3test.Object{
		"guides/index.html":    {Content: []byte("<html>guides</html>"), ETag: `"etag-g"`, ContentType: "text/html"},
		"guides/assets/app.js": {Content: []byte("console.log('ok')"), ETag: `"etag-a"`, ContentType: "text/javascript"},
		"docs":                 {Content: []byte("Annual report body"), ETag: `"etag-d"`, ContentType: "text/plain"},
		"notes/Index.html":     {Content: []byte("<html>notes</html>"), ETag: `"etag-n"`, ContentType: "text/html"},
	}
	bucket := s3test.NewServer("page-hub", objects)
	defer bucket.Close()

	report, err := storage.CheckCompatibility(context.Background(), compatReader(t, bucket))
	if err != nil {
		t.Fatalf("CheckCompatibility: %v", err)
	}

	if report.Objects != 4 {
		t.Fatalf("objects = %d, want 4", report.Objects)
	}
	wantBytes := int64(len("<html>guides</html>") + len("console.log('ok')") + len("Annual report body") + len("<html>notes</html>"))
	if report.TotalBytes != wantBytes {
		t.Fatalf("totalBytes = %d, want %d", report.TotalBytes, wantBytes)
	}
	// Every object body must download completely for a small bucket.
	if len(report.DownloadedKeys) != 4 {
		t.Fatalf("downloadedKeys = %v, want every key downloaded", report.DownloadedKeys)
	}
	for index, key := range report.DownloadedKeys {
		if index > 0 && key <= report.DownloadedKeys[index-1] {
			t.Fatalf("downloadedKeys = %v, want deterministic sorted order", report.DownloadedKeys)
		}
	}

	// The compatibility check is read-only: only GET and HEAD requests may
	// reach storage.
	bucket.AssertOnlyReads(t)
}

func TestCheckCompatibilityBoundsDownloadedBodies(t *testing.T) {
	objects := map[string]s3test.Object{}
	for index := 0; index < 40; index++ {
		objects[fmt.Sprintf("publications/object-%02d", index)] = s3test.Object{
			Content: []byte{byte(index)},
			ETag:    `"etag"`,
		}
	}
	bucket := s3test.NewServer("page-hub", objects)
	defer bucket.Close()

	report, err := storage.CheckCompatibility(context.Background(), compatReader(t, bucket))
	if err != nil {
		t.Fatalf("CheckCompatibility: %v", err)
	}

	if report.Objects != 40 {
		t.Fatalf("objects = %d, want 40", report.Objects)
	}
	if len(report.DownloadedKeys) == 0 || len(report.DownloadedKeys) >= 40 {
		t.Fatalf("downloadedKeys = %d entries, want a bounded sample", len(report.DownloadedKeys))
	}
}

func TestCheckCompatibilityHandlesEmptyBucket(t *testing.T) {
	bucket := s3test.NewServer("page-hub", map[string]s3test.Object{})
	defer bucket.Close()

	report, err := storage.CheckCompatibility(context.Background(), compatReader(t, bucket))
	if err != nil {
		t.Fatalf("CheckCompatibility: %v", err)
	}
	if report.Objects != 0 || report.TotalBytes != 0 || len(report.DownloadedKeys) != 0 {
		t.Fatalf("report = %+v, want an empty compatible bucket", report)
	}
}

func TestCheckCompatibilityFailsClosedWhenStorageUnavailable(t *testing.T) {
	reader := storage.NewS3Reader(storage.Config{
		Endpoint:        "http://127.0.0.1:1",
		Region:          "test-region",
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})

	_, err := storage.CheckCompatibility(context.Background(), reader)
	if !errors.Is(err, storage.ErrStorageUnavailable) {
		t.Fatalf("error = %v, want ErrStorageUnavailable", err)
	}
}

// failingReadReader lists objects but fails every body read: a deterministic
// failure the in-process S3 server cannot produce.
type failingReadReader struct {
	keys []storage.ObjectListing
}

func (f *failingReadReader) ListObjects(context.Context) ([]storage.ObjectListing, error) {
	return f.keys, nil
}

func (f *failingReadReader) ReadObject(context.Context, string) (storage.ObjectContent, error) {
	return storage.ObjectContent{}, storage.ErrStorageUnavailable
}

func (f *failingReadReader) StatObject(context.Context, string) (storage.ObjectMeta, error) {
	return storage.ObjectMeta{}, storage.ErrStorageUnavailable
}

func TestCheckCompatibilityFailsWhenBodyReadFails(t *testing.T) {
	reader := &failingReadReader{keys: []storage.ObjectListing{
		{Key: "docs", Size: 18, LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}}

	_, err := storage.CheckCompatibility(context.Background(), reader)
	if err == nil {
		t.Fatal("a failing body read must fail the compatibility check")
	}
	if !errors.Is(err, storage.ErrStorageUnavailable) {
		t.Fatalf("error = %v, want ErrStorageUnavailable", err)
	}
}
