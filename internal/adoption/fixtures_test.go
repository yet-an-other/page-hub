package adoption

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

// strictUnmarshal rejects unknown fields, mirroring the published schema's
// additionalProperties: false.
func strictUnmarshal(encoded []byte, target *Declaration) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// TestRepresentativeFixturesValidate proves the published fixtures parse,
// satisfy the declaration rules, and plan against matching storage.
func TestRepresentativeFixturesValidate(t *testing.T) {
	fixtures := map[string]map[string]s3test.Object{
		"single-directory-index.json": {
			"guides/getting-started/index.html": {
				Content:      []byte("<html>start</html>"),
				ContentType:  "text/html",
				LastModified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			"guides/getting-started/app.js": {
				Content:      []byte("console.log('ready')"),
				ContentType:  "text/javascript",
				LastModified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		"exact-file-uppercase.json": {
			"docs/Annual-Report.TXT": {
				Content:      []byte("Annual report"),
				ContentType:  "text/plain",
				LastModified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no representative fixtures found")
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			objects, known := fixtures[entry.Name()]
			if !known {
				t.Fatalf("fixture %q has no matching storage objects", entry.Name())
			}
			encoded, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var declaration Declaration
			if err := strictUnmarshal(encoded, &declaration); err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			if err := declaration.Validate(); err != nil {
				t.Fatalf("fixture declaration is invalid: %v", err)
			}
			bucket := s3test.NewServer("page-hub", objects)
			defer bucket.Close()
			reader := storage.NewS3Reader(storage.Config{
				Endpoint:        bucket.URL(),
				Bucket:          "page-hub",
				AccessKeyID:     "test-access-key",
				SecretAccessKey: "test-secret-key",
			})
			plan, err := BuildPlan(context.Background(), reader, declaration)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if err := plan.Verify(); err != nil {
				t.Fatalf("plan digest does not verify: %v", err)
			}
		})
	}
}
