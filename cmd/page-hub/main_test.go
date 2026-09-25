package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestVersionCommandReportsVersionAndSchemaRange(t *testing.T) {
	if testing.Short() {
		t.Skip("building the CLI binary is too slow for -short runs")
	}
	binary := buildBinary(t)

	for _, invocation := range [][]string{{"version"}, {"-version"}} {
		stdout, stderr, err := runCLI(t, binary, nil, invocation...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", invocation, err, stderr)
		}
		if !strings.Contains(stdout, "page-hub ") {
			t.Fatalf("%s output = %q, want a version line", invocation, stdout)
		}
		if !strings.Contains(stdout, catalog.SupportedSchemaRange()) {
			t.Fatalf("%s output = %q, want the compatible catalog schema range", invocation, stdout)
		}
		if strings.Contains(stderr, "secret") {
			t.Fatalf("%s stderr = %q", invocation, stderr)
		}
	}
}

func TestCLICheckRunsReadOnlyCompatibilityChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("building the CLI binary is too slow for -short runs")
	}
	binary := buildBinary(t)

	objects := map[string]s3test.Object{
		"guides/getting-started/index.html": {Content: []byte("<html>getting started</html>"), ETag: `"check-html"`},
		"guides/getting-started/app.js":     {Content: []byte("console.log('ready')"), ETag: `"check-js"`},
		"docs":                              {Content: []byte("Annual report body"), ETag: `"check-docs"`},
	}
	bucket := s3test.NewServer("page-hub", objects)
	defer bucket.Close()

	// The compatibility check needs only storage settings: no catalog path,
	// no assertion, no quota, and no public base URL.
	env := []string{
		fmt.Sprintf("PAGE_HUB_S3_ENDPOINT=%s", bucket.URL()),
		"PAGE_HUB_S3_BUCKET=page-hub",
		"PAGE_HUB_S3_ACCESS_KEY_ID=test-access-key",
		"PAGE_HUB_S3_SECRET_ACCESS_KEY=test-secret-key",
	}
	stdout, stderr, err := runCLI(t, binary, env, "check")
	if err != nil {
		t.Fatalf("check: %v\n%s", err, stderr)
	}

	var report struct {
		Objects        int      `json:"objects"`
		TotalBytes     int64    `json:"totalBytes"`
		DownloadedKeys []string `json:"downloadedKeys"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse check output %q: %v", stdout, err)
	}
	if report.Objects != 3 || report.TotalBytes != int64(len("<html>getting started</html>")+len("console.log('ready')")+len("Annual report body")) {
		t.Fatalf("check report = %+v", report)
	}
	if len(report.DownloadedKeys) != 3 {
		t.Fatalf("downloadedKeys = %v, want every object downloaded", report.DownloadedKeys)
	}

	// The check never writes, copies, or deletes.
	bucket.AssertOnlyReads(t)
}

func TestCLICheckFailsClosedWithoutTouchingTheCatalogOrLeakingSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("building the CLI binary is too slow for -short runs")
	}
	binary := buildBinary(t)

	env := []string{
		"PAGE_HUB_S3_ENDPOINT=http://127.0.0.1:1",
		"PAGE_HUB_S3_BUCKET=page-hub",
		"PAGE_HUB_S3_ACCESS_KEY_ID=secret-access-key-id",
		"PAGE_HUB_S3_SECRET_ACCESS_KEY=secret-access-key-value",
		"PAGE_HUB_CATALOG_PATH=" + filepath.Join(t.TempDir(), "catalog.db"),
	}
	stdout, stderr, err := runCLI(t, binary, env, "check")
	if err == nil {
		t.Fatalf("check against unavailable storage succeeded: %s", stdout)
	}
	for _, output := range []string{stdout, stderr} {
		if strings.Contains(output, "secret-access-key") {
			t.Fatalf("check output leaked credentials: %q", output)
		}
	}
	if _, err := os.Stat(env[4][len("PAGE_HUB_CATALOG_PATH="):]); !os.IsNotExist(err) {
		t.Fatal("the compatibility check must not create or touch catalog state")
	}
}

// fakeSite stands in for the public routing layer during CLI tests.
type fakeSite struct {
	server *httptest.Server

	mu     sync.Mutex
	routes map[string]fakeRoute
}

type fakeRoute struct {
	status      int
	contentType string
	body        []byte
}

func newFakeSite(t *testing.T, routes map[string]fakeRoute) *fakeSite {
	t.Helper()
	site := &fakeSite{routes: routes}
	site.server = httptest.NewServer(http.HandlerFunc(site.serve))
	t.Cleanup(site.server.Close)
	return site
}

func (s *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	route, ok := s.routes[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", route.contentType)
	w.WriteHeader(route.status)
	_, _ = w.Write(route.body)
}

func (s *fakeSite) URL() string { return s.server.URL }

func TestCLIAdoptionEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("building the CLI binary is too slow for -short runs")
	}
	binary := buildBinary(t)

	indexBody := []byte("<html><body>getting started</body></html>")
	objects := map[string]s3test.Object{
		"guides/getting-started/index.html": {
			Content:      indexBody,
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
		"docs": {
			Content:      []byte("Annual report body"),
			ContentType:  "text/plain",
			LastModified: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			ETag:         `"cli-etag-docs"`,
		},
	}
	bucket := s3test.NewServer("page-hub", objects)
	defer bucket.Close()

	site := newFakeSite(t, map[string]fakeRoute{
		"/guides/getting-started": {status: http.StatusOK, contentType: "text/html", body: indexBody},
		"/docs":                   {status: http.StatusOK, contentType: "text/plain", body: []byte("Annual report body")},
	})

	workDir := t.TempDir()
	catalogPath := filepath.Join(workDir, "catalog.db")
	declarationPath := filepath.Join(workDir, "declaration.json")
	declaration := map[string]any{
		"version": 1,
		"publications": []map[string]any{
			{
				"project":     map[string]any{"prefix": "guides", "displayName": "Guides"},
				"path":        "guides/getting-started",
				"displayName": "Getting Started",
				"entryPoint":  "guides/getting-started/index.html",
				"routingMode": "directory_index",
				"objects":     []string{"guides/getting-started/index.html", "guides/getting-started/app.js"},
				"probes": []map[string]any{
					{"path": "", "expectStatus": 200},
				},
			},
			{
				"project":     map[string]any{"prefix": "docs"},
				"path":        "docs",
				"entryPoint":  "docs",
				"routingMode": "exact_file",
				"objects":     []string{"docs"},
				"probes":      []map[string]any{{"path": "", "expectStatus": 200}},
			},
		},
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
		fmt.Sprintf("PAGE_HUB_PUBLIC_BASE_URL=%s", site.URL()),
	}

	// The runtime refuses an unmigrated catalog; the explicit migration
	// command prepares it.
	if _, stderr, err := runCLI(t, binary, env, "migrate", "-catalog", catalogPath); err != nil {
		t.Fatalf("migrate: %v\n%s", err, stderr)
	}

	// Planning fails closed without the public base URL.
	if _, _, err := runCLI(t, binary, env[:len(env)-1], "plan", "-declaration", declarationPath, "-out", planPath); err == nil {
		t.Fatal("plan without PAGE_HUB_PUBLIC_BASE_URL should fail")
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
		Digest  string `json:"digest"`
		Content struct {
			PublicBaseURL string `json:"publicBaseURL"`
			Publications  []struct {
				Path         string `json:"path"`
				CanonicalURL string `json:"canonicalUrl"`
				Probes       []struct {
					URL            string `json:"url"`
					ExpectStatus   int    `json:"expectStatus"`
					ObservedStatus int    `json:"observedStatus"`
					BodySHA256     string `json:"bodySha256"`
				} `json:"probes"`
			} `json:"publications"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(planEncoded, &plan); err != nil {
		t.Fatalf("parse plan: %v", err)
	}
	if len(plan.Digest) != len("sha256:")+64 {
		t.Fatalf("plan digest = %q", plan.Digest)
	}
	if len(plan.Content.Publications) != 2 {
		t.Fatalf("plan declares %d publications, want 2", len(plan.Content.Publications))
	}
	for _, publication := range plan.Content.Publications {
		if publication.CanonicalURL != site.URL()+"/"+publication.Path {
			t.Fatalf("canonical URL = %q for path %q", publication.CanonicalURL, publication.Path)
		}
		if len(publication.Probes) != 1 || publication.Probes[0].ObservedStatus != http.StatusOK || publication.Probes[0].BodySHA256 == "" {
			t.Fatalf("probes = %+v for path %q", publication.Probes, publication.Path)
		}
	}

	// Committing requires the approved plan and an operation ID, and refuses
	// a public base URL that differs from the plan's recorded origin.
	operationID := catalog.NewID()
	mismatchEnv := make([]string, len(env))
	copy(mismatchEnv, env)
	mismatchEnv[len(mismatchEnv)-1] = "PAGE_HUB_PUBLIC_BASE_URL=https://elsewhere.example"
	if _, stderr, err := runCLI(t, binary, mismatchEnv, "commit", "-plan", planPath, "-operation-id", operationID, "-catalog", catalogPath); err == nil {
		t.Fatal("commit with a mismatched public base URL should fail")
	} else if !strings.Contains(stderr, "elsewhere.example") {
		t.Fatalf("mismatch commit stderr = %q", stderr)
	}
	commitStdout, stderr, err := runCLI(t, binary, env, "commit", "-plan", planPath, "-operation-id", operationID, "-catalog", catalogPath)
	if err != nil {
		t.Fatalf("commit: %v\n%s", err, stderr)
	}
	var commitResult struct {
		Result struct {
			Status      string `json:"status"`
			ObjectCount int    `json:"objectCount"`
			Projects    []struct {
				ProjectPrefix string `json:"projectPrefix"`
				Publications  []struct {
					PublicationPath string `json:"publicationPath"`
				} `json:"publications"`
			} `json:"projects"`
		}
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(commitStdout), &commitResult); err != nil {
		t.Fatalf("parse commit output %q: %v", commitStdout, err)
	}
	if commitResult.Replayed || commitResult.Result.Status != "committed" {
		t.Fatalf("commit result = %+v", commitResult)
	}
	if commitResult.Result.ObjectCount != 3 || len(commitResult.Result.Projects) != 2 {
		t.Fatalf("commit result = %+v", commitResult.Result)
	}

	// The whole workflow only ever read from storage.
	bucket.AssertOnlyReads(t)

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
	if len(inventory.Projects) != 2 {
		t.Fatalf("inventory = %+v, want 2 projects", inventory)
	}
	byPrefix := map[string]int{}
	for index, project := range inventory.Projects {
		byPrefix[project.Prefix] = index
	}
	if inventory.Projects[byPrefix["guides"]].Publications[0].Path != "guides/getting-started" {
		t.Fatalf("guides publications = %+v", inventory.Projects[byPrefix["guides"]].Publications)
	}
	if got := inventory.Projects[byPrefix["docs"]].Publications; len(got) != 1 || got[0].Path != "docs" {
		t.Fatalf("docs publications = %+v", got)
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

func TestCLICheckRejectsUnexpectedArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("building the CLI binary is too slow for -short runs")
	}
	binary := buildBinary(t)
	if _, _, err := runCLI(t, binary, nil, "check", "extra"); err == nil {
		t.Fatal("check with unexpected arguments should fail")
	}
}
