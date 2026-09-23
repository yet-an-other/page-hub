package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

// buildBinary compiles the production CLI once for this test run.
func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "page-hub")
	command := exec.Command("go", "build", "-o", binary, ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return binary
}

func runCLI(t *testing.T, binary string, env []string, args ...string) (string, string, error) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func TestCLIAdoptionEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("building the CLI binary is too slow for -short runs")
	}
	binary := buildBinary(t)

	objects := map[string]s3test.Object{
		"guides/getting-started/index.html": {
			Content:      []byte("<html><body>getting started</body></html>"),
			ContentType:  "text/html; charset=utf-8",
			UserMetadata: map[string]string{"source": "legacy"},
			LastModified: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
			ETag:         `"cli-etag-html"`,
		},
		"guides/getting-started/app.js": {
			Content:      []byte("console.log('ready')"),
			ContentType:  "text/javascript",
			LastModified: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC),
			ETag:         `"cli-etag-js"`,
		},
	}
	bucket := s3test.NewServer("page-hub", objects)
	defer bucket.Close()

	workDir := t.TempDir()
	catalogPath := filepath.Join(workDir, "catalog.db")
	declarationPath := filepath.Join(workDir, "declaration.json")
	declaration := map[string]any{
		"version": 1,
		"publications": []map[string]any{{
			"project":     map[string]any{"prefix": "guides", "displayName": "Guides"},
			"path":        "guides/getting-started",
			"displayName": "Getting Started",
			"entryPoint":  "guides/getting-started/index.html",
			"routingMode": "directory_index",
			"objects":     []string{"guides/getting-started/index.html", "guides/getting-started/app.js"},
		}},
	}
	encoded, err := json.Marshal(declaration)
	if err != nil {
		t.Fatalf("encode declaration: %v", err)
	}
	if err := os.WriteFile(declarationPath, encoded, 0o600); err != nil {
		t.Fatalf("write declaration: %v", err)
	}

	planPath := filepath.Join(workDir, "plan.json")
	env := []string{
		fmt.Sprintf("PAGE_HUB_S3_ENDPOINT=%s", bucket.URL()),
		"PAGE_HUB_S3_BUCKET=page-hub",
		"PAGE_HUB_S3_ACCESS_KEY_ID=test-access-key",
		"PAGE_HUB_S3_SECRET_ACCESS_KEY=test-secret-key",
	}

	// The runtime refuses an unmigrated catalog; the explicit migration
	// command prepares it.
	if _, stderr, err := runCLI(t, binary, env, "migrate", "-catalog", catalogPath); err != nil {
		t.Fatalf("migrate: %v\n%s", err, stderr)
	}

	// Planning emits reviewable JSON with a digest and changes nothing.
	stdout, stderr, err := runCLI(t, binary, env, "plan", "-declaration", declarationPath, "-out", planPath)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	_ = stdout
	planEncoded, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	var plan struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(planEncoded, &plan); err != nil {
		t.Fatalf("parse plan: %v", err)
	}
	if len(plan.Digest) != len("sha256:")+64 {
		t.Fatalf("plan digest = %q", plan.Digest)
	}

	// Committing requires the approved plan and an operation ID.
	operationID := catalog.NewID()
	commitStdout, stderr, err := runCLI(t, binary, env, "commit", "-plan", planPath, "-operation-id", operationID, "-catalog", catalogPath)
	if err != nil {
		t.Fatalf("commit: %v\n%s", err, stderr)
	}
	var commitResult struct {
		Result struct {
			Status        string `json:"status"`
			ProjectPrefix string `json:"projectPrefix"`
			AcceptedSize  int64  `json:"acceptedSize"`
		}
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(commitStdout), &commitResult); err != nil {
		t.Fatalf("parse commit output %q: %v", commitStdout, err)
	}
	if commitResult.Replayed || commitResult.Result.Status != "committed" || commitResult.Result.ProjectPrefix != "guides" {
		t.Fatalf("commit result = %+v", commitResult)
	}

	// The whole workflow only ever read from storage.
	for _, request := range bucket.Requests() {
		switch request.Method {
		case http.MethodGet, http.MethodHead:
		default:
			t.Fatalf("storage request %s %s mutates storage", request.Method, request.Path)
		}
	}

	// A restarted manager sees the accepted state.
	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(t.Context())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inventory) != 1 || inventory[0].Prefix != "guides" || len(inventory[0].Publications) != 1 {
		t.Fatalf("inventory = %+v", inventory)
	}
	if inventory[0].Publications[0].Path != "guides/getting-started" {
		t.Fatalf("publication = %+v", inventory[0].Publications[0])
	}

	// Re-running the same operation ID replays the durable result.
	replayStdout, stderr, err := runCLI(t, binary, env, "commit", "-plan", planPath, "-operation-id", operationID, "-catalog", catalogPath)
	if err != nil {
		t.Fatalf("commit replay: %v\n%s", err, stderr)
	}
	var replay struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(replayStdout), &replay); err != nil {
		t.Fatalf("parse replay output %q: %v", replayStdout, err)
	}
	if !replay.Replayed {
		t.Fatal("expected the replayed durable result")
	}
}
