package adoption

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/internal/storage/s3test"
)

// singleDeclaration declares one directory-index Publication with two
// objects and one public-route probe.
func singleDeclaration() Declaration {
	return Declaration{
		Version: 1,
		Publications: []PublicationDeclaration{{
			Project:     ProjectDeclaration{Prefix: "notes", DisplayName: "Notes"},
			Path:        "notes/2026-report",
			DisplayName: "2026 Report",
			EntryPoint:  "notes/2026-report/index.html",
			RoutingMode: RoutingDirectoryIndex,
			Objects:     []string{"notes/2026-report/index.html", "notes/2026-report/style.css"},
			Probes:      []RouteProbe{{Path: "", ExpectStatus: http.StatusOK}},
		}},
	}
}

// heterogeneousDeclaration declares the representative layout classes: three
// Projects, a multi-object fallback root Publication, a multi-object
// directory-index Publication with a case-sensitive entry point, and a
// single-object exact-file root Publication whose object key is the path
// itself, with mixed probe expectations.
func heterogeneousDeclaration() Declaration {
	return Declaration{
		Version: 1,
		Publications: []PublicationDeclaration{
			{
				Project:     ProjectDeclaration{Prefix: "guides"},
				Path:        "guides",
				EntryPoint:  "guides/index.html",
				RoutingMode: RoutingFallback,
				Objects:     []string{"guides/index.html", "guides/assets/style.css"},
				Probes: []RouteProbe{
					{Path: "", ExpectStatus: http.StatusOK},
					{Path: "anything", ExpectStatus: http.StatusOK},
				},
			},
			{
				Project:     ProjectDeclaration{Prefix: "reports"},
				Path:        "reports",
				EntryPoint:  "reports/Index.html",
				RoutingMode: RoutingDirectoryIndex,
				Objects:     []string{"reports/Index.html", "reports/app.js"},
				Probes: []RouteProbe{
					{Path: "", ExpectStatus: http.StatusOK},
					{Path: "deep/link", ExpectStatus: http.StatusNotFound},
				},
			},
			{
				Project:     ProjectDeclaration{Prefix: "docs"},
				Path:        "docs",
				EntryPoint:  "docs",
				RoutingMode: RoutingExactFile,
				Objects:     []string{"docs"},
				Probes:      []RouteProbe{{Path: "", ExpectStatus: http.StatusOK}},
			},
		},
	}
}

func heterogeneousObjects() map[string]s3test.Object {
	return map[string]s3test.Object{
		"guides/index.html": {
			Content:      []byte("<html><body>guides home</body></html>"),
			ContentType:  "text/html; charset=utf-8",
			UserMetadata: map[string]string{"source": "legacy-share"},
			LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ETag:         `"etag-guides-index"`,
		},
		"guides/assets/style.css": {
			Content:      []byte("body { color: black }"),
			ContentType:  "text/css",
			LastModified: time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC),
			ETag:         `"etag-style"`,
		},
		"reports/Index.html": {
			Content:      []byte("<html><body>Reports</body></html>"),
			ContentType:  "text/html",
			LastModified: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			ETag:         `"etag-reports-index"`,
		},
		"reports/app.js": {
			Content:      []byte("console.log('ready')"),
			ContentType:  "text/javascript",
			LastModified: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			ETag:         `"etag-app"`,
		},
		"docs": {
			Content:      []byte("Annual report body"),
			ContentType:  "text/plain",
			LastModified: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			ETag:         `"etag-docs"`,
		},
	}
}

// heterogeneousSite routes the public probes the heterogeneous declaration
// expects.
func heterogeneousSite(t *testing.T) *fakeSite {
	t.Helper()
	site := newFakeSite(t)
	site.setRoute("/guides", siteResponse{status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>guides home</body></html>")})
	site.setRoute("/guides/anything", siteResponse{status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>guides home</body></html>")})
	site.setRoute("/reports", siteResponse{status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>Reports</body></html>")})
	site.setRoute("/reports/deep/link", siteResponse{status: http.StatusNotFound, contentType: "text/plain", body: []byte("not found")})
	site.setRoute("/docs", siteResponse{status: http.StatusOK, contentType: "text/plain", body: []byte("Annual report body")})
	return site
}

func fakeBucket(t *testing.T, objects map[string]s3test.Object) (*s3test.Server, storage.Reader) {
	t.Helper()
	bucket := s3test.NewServer("page-hub", objects)
	t.Cleanup(bucket.Close)
	reader := storage.NewS3Reader(storage.Config{
		Endpoint:        bucket.URL(),
		Bucket:          "page-hub",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	return bucket, reader
}

func buildPlan(t *testing.T, reader storage.Reader, site *fakeSite, declaration Declaration) (Plan, error) {
	t.Helper()
	prober := site.prober()
	return BuildPlan(context.Background(), reader, prober, site.baseURL(), declaration)
}

func migratedCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	if _, _, err := catalog.Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return path
}

func assertOnlyReadRequests(t *testing.T, bucket *s3test.Server) {
	t.Helper()
	for _, request := range bucket.Requests() {
		switch request.Method {
		case http.MethodGet, http.MethodHead:
		default:
			t.Fatalf("storage request %s %s mutates storage", request.Method, request.Path)
		}
	}
}

func TestDeclarationValidation(t *testing.T) {
	if err := singleDeclaration().Validate(); err != nil {
		t.Fatalf("valid declaration rejected: %v", err)
	}

	cases := map[string]func(*Declaration){
		"reserved manager prefix": func(d *Declaration) { d.Publications[0].Project.Prefix = "_page-hub" },
		"reserved prefix case":    func(d *Declaration) { d.Publications[0].Project.Prefix = "_PAGE-HUB" },
		"prefix with slash":       func(d *Declaration) { d.Publications[0].Project.Prefix = "nested/prefix" },
		"path outside prefix":     func(d *Declaration) { d.Publications[0].Path = "elsewhere/report" },
		"entry point not declared": func(d *Declaration) {
			d.Publications[0].EntryPoint = "notes/2026-report/missing.html"
		},
		"exact file with two objects": func(d *Declaration) {
			d.Publications[0].RoutingMode = RoutingExactFile
		},
		"duplicate object keys": func(d *Declaration) {
			d.Publications[0].Objects = append(d.Publications[0].Objects, d.Publications[0].Objects[0])
		},
		"empty object set": func(d *Declaration) { d.Publications[0].Objects = nil },
		"invalid routing mode": func(d *Declaration) {
			d.Publications[0].RoutingMode = RoutingMode("sync")
		},
		"object outside publication path": func(d *Declaration) {
			d.Publications[0].Objects = append(d.Publications[0].Objects, "other/file.html")
		},
		"empty version":   func(d *Declaration) { d.Version = 2 },
		"no publications": func(d *Declaration) { d.Publications = nil },
		"no probes":       func(d *Declaration) { d.Publications[0].Probes = nil },
		"probe without expectation": func(d *Declaration) {
			d.Publications[0].Probes = []RouteProbe{{Path: ""}}
		},
		"probe expectation out of range": func(d *Declaration) {
			d.Publications[0].Probes = []RouteProbe{{Path: "", ExpectStatus: 42}}
		},
		"duplicate probe paths": func(d *Declaration) {
			d.Publications[0].Probes = []RouteProbe{{Path: "", ExpectStatus: 200}, {Path: "", ExpectStatus: 404}}
		},
		"probe path with leading slash": func(d *Declaration) {
			d.Publications[0].Probes = []RouteProbe{{Path: "/deep", ExpectStatus: 200}}
		},
		"probe path with dot segment": func(d *Declaration) {
			d.Publications[0].Probes = []RouteProbe{{Path: "../escape", ExpectStatus: 200}}
		},
		"conflicting object ownership": func(d *Declaration) {
			d.Publications = append(d.Publications, PublicationDeclaration{
				Project:     ProjectDeclaration{Prefix: "notes"},
				Path:        "notes/archive",
				EntryPoint:  "notes/archive/index.html",
				RoutingMode: RoutingDirectoryIndex,
				Objects:     []string{"notes/2026-report/index.html"},
				Probes:      []RouteProbe{{Path: "", ExpectStatus: 200}},
			})
		},
		"conflicting project display names": func(d *Declaration) {
			d.Publications = append(d.Publications, PublicationDeclaration{
				Project:     ProjectDeclaration{Prefix: "notes", DisplayName: "Something else"},
				Path:        "notes/archive",
				EntryPoint:  "notes/archive/index.html",
				RoutingMode: RoutingDirectoryIndex,
				Objects:     []string{"notes/archive/index.html"},
				Probes:      []RouteProbe{{Path: "", ExpectStatus: 200}},
			})
		},
	}
	for name, mutate := range cases {
		declaration := singleDeclaration()
		mutate(&declaration)
		if err := declaration.Validate(); err == nil {
			t.Errorf("%s: declaration should be rejected", name)
		}
	}
}

func TestDeclarationRejectsAmbiguousBoundariesAndOverlappingRoutes(t *testing.T) {
	base := heterogeneousDeclaration()
	if err := base.Validate(); err != nil {
		t.Fatalf("heterogeneous declaration rejected: %v", err)
	}

	cases := map[string]func(*Declaration){
		"nested publication path": func(d *Declaration) {
			d.Publications = append(d.Publications, PublicationDeclaration{
				Project:     ProjectDeclaration{Prefix: "reports"},
				Path:        "reports/deep",
				EntryPoint:  "reports/deep/page.html",
				RoutingMode: RoutingExactFile,
				Objects:     []string{"reports/deep/page.html"},
				Probes:      []RouteProbe{{Path: "", ExpectStatus: 200}},
			})
		},
		"publication nested inside root publication": func(d *Declaration) {
			d.Publications = append(d.Publications, PublicationDeclaration{
				Project:     ProjectDeclaration{Prefix: "docs"},
				Path:        "docs/handbook",
				EntryPoint:  "docs/handbook/index.html",
				RoutingMode: RoutingDirectoryIndex,
				Objects:     []string{"docs/handbook/index.html"},
				Probes:      []RouteProbe{{Path: "", ExpectStatus: 200}},
			})
		},
		"duplicate publication path": func(d *Declaration) {
			duplicated := d.Publications[0]
			duplicated.Probes = []RouteProbe{{Path: "", ExpectStatus: 200}}
			d.Publications = append(d.Publications, duplicated)
		},
	}
	for name, mutate := range cases {
		declaration := heterogeneousDeclaration()
		mutate(&declaration)
		if err := declaration.Validate(); err == nil {
			t.Errorf("%s: declaration should be rejected", name)
		}
	}
}

func TestDeclarationPreservesExactCase(t *testing.T) {
	declaration := Declaration{
		Version: 1,
		Publications: []PublicationDeclaration{{
			Project:     ProjectDeclaration{Prefix: "docs"},
			Path:        "docs/Annual-Report.TXT",
			EntryPoint:  "docs/Annual-Report.TXT",
			RoutingMode: RoutingExactFile,
			Objects:     []string{"docs/Annual-Report.TXT"},
			Probes:      []RouteProbe{{Path: "", ExpectStatus: http.StatusOK}},
		}},
	}
	if err := declaration.Validate(); err != nil {
		t.Fatalf("uppercase exact-file declaration rejected: %v", err)
	}
	if got := RelativePath("docs/Annual-Report.TXT", "docs/Annual-Report.TXT"); got != "Annual-Report.TXT" {
		t.Fatalf("relative path = %q", got)
	}
}

func TestPlanRecordsExactObservedState(t *testing.T) {
	bucket, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := plan.Verify(); err != nil {
		t.Fatalf("plan digest does not verify: %v", err)
	}
	if plan.Content.PublicBaseURL != site.baseURL() {
		t.Fatalf("plan public base URL = %q", plan.Content.PublicBaseURL)
	}

	if len(plan.Content.Publications) != 3 {
		t.Fatalf("planned %d publications, want 3", len(plan.Content.Publications))
	}

	// The fallback root Publication with its canonical URL and case-exact
	// entry point.
	fallback := plan.Content.Publications[0]
	if fallback.Path != "guides" || fallback.EntryPoint != "guides/index.html" || fallback.RoutingMode != RoutingFallback {
		t.Fatalf("fallback publication = %+v", fallback)
	}
	if fallback.CanonicalURL != site.baseURL()+"/guides" {
		t.Fatalf("fallback canonical URL = %q", fallback.CanonicalURL)
	}
	if fallback.ContentChangedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("content changed at = %q, want the newest object modification time", fallback.ContentChangedAt)
	}
	if len(fallback.Objects) != 2 {
		t.Fatalf("planned %d fallback objects, want 2", len(fallback.Objects))
	}
	indexObject := heterogeneousObjects()["guides/index.html"]
	var index PlannedObject
	for _, object := range fallback.Objects {
		if object.Key == "guides/index.html" {
			index = object
		}
	}
	if index.Key == "" {
		t.Fatalf("index object missing from plan: %+v", fallback.Objects)
	}
	if index.RelativePath != "index.html" || index.Size != int64(len(indexObject.Content)) || index.ETag != `"etag-guides-index"` {
		t.Fatalf("index object = %+v", index)
	}
	if index.ContentType != "text/html; charset=utf-8" || index.UserMetadata["source"] != "legacy-share" {
		t.Fatalf("index object = %+v", index)
	}
	if len(index.SHA256) != 64 {
		t.Fatalf("index object sha256 = %q", index.SHA256)
	}

	// The case-sensitive directory-index Publication.
	caseSensitive := plan.Content.Publications[1]
	if caseSensitive.Path != "reports" || caseSensitive.EntryPoint != "reports/Index.html" {
		t.Fatalf("case-sensitive publication = %+v", caseSensitive)
	}
	if caseSensitive.CanonicalURL != site.baseURL()+"/reports" {
		t.Fatalf("case-sensitive canonical URL = %q", caseSensitive.CanonicalURL)
	}

	// The exact-file root Publication whose object key is the path itself.
	exact := plan.Content.Publications[2]
	if exact.Path != "docs" || exact.RoutingMode != RoutingExactFile || len(exact.Objects) != 1 {
		t.Fatalf("exact publication = %+v", exact)
	}
	if exact.Objects[0].Key != "docs" || exact.Objects[0].RelativePath != "docs" {
		t.Fatalf("exact object = %+v", exact.Objects[0])
	}
	if exact.CanonicalURL != site.baseURL()+"/docs" {
		t.Fatalf("exact canonical URL = %q", exact.CanonicalURL)
	}

	assertOnlyReadRequests(t, bucket)
}

func TestPlanProbesDeclaredPublicRoutes(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	fallback := plan.Content.Publications[0]
	if len(fallback.Probes) != 2 {
		t.Fatalf("fallback probes = %+v", fallback.Probes)
	}
	rootProbe := fallback.Probes[0]
	if rootProbe.Path != "" || rootProbe.URL != site.baseURL()+"/guides" || rootProbe.ExpectStatus != http.StatusOK || rootProbe.ObservedStatus != http.StatusOK {
		t.Fatalf("root probe = %+v", rootProbe)
	}
	if rootProbe.ContentType != "text/html" || rootProbe.BodySHA256 == "" {
		t.Fatalf("root probe observation incomplete: %+v", rootProbe)
	}
	fallbackProbe := fallback.Probes[1]
	if fallbackProbe.Path != "anything" || fallbackProbe.URL != site.baseURL()+"/guides/anything" || fallbackProbe.ObservedStatus != http.StatusOK {
		t.Fatalf("fallback probe = %+v", fallbackProbe)
	}

	caseSensitive := plan.Content.Publications[1]
	if len(caseSensitive.Probes) != 2 {
		t.Fatalf("case-sensitive probes = %+v", caseSensitive.Probes)
	}
	if caseSensitive.Probes[1].ObservedStatus != http.StatusNotFound {
		t.Fatalf("404 probe = %+v", caseSensitive.Probes[1])
	}

	// Every probe was a plain GET against the public site.
	for _, request := range site.requests() {
		if request.method != http.MethodGet {
			t.Fatalf("public probe %s %s is not a read", request.method, request.path)
		}
	}

	// A probe whose observed behavior differs from the declared expectation
	// rejects the plan.
	declaration := heterogeneousDeclaration()
	site.setRoute("/docs", siteResponse{status: http.StatusNotFound, contentType: "text/plain", body: []byte("not found")})
	if _, err := buildPlan(t, reader, site, declaration); err == nil {
		t.Fatal("a probe result contradicting its expectation should reject the plan")
	}

	// An unreachable public site rejects the plan.
	down := newFakeSite(t)
	down.close()
	if _, err := BuildPlan(context.Background(), reader, down.prober(), site.baseURL(), declaration); err == nil {
		t.Fatal("an unreachable public site should reject the plan")
	}
}

func TestPlanDigestBindsProbeResults(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Planning the identical declaration against identical storage and the
	// identical public routes produces the identical digest.
	again, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() again error = %v", err)
	}
	if again.Digest != plan.Digest {
		t.Fatal("identical probes produced different plan digests")
	}

	// A changed probe body changes the digest.
	site.setRoute("/guides", siteResponse{status: http.StatusOK, contentType: "text/html", body: []byte("<html>changed</html>")})
	changed, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() on changed site error = %v", err)
	}
	if changed.Digest == plan.Digest {
		t.Fatal("a changed probe body produced the approved digest")
	}
	site.setRoute("/guides", siteResponse{status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>guides home</body></html>")})

	// A changed public base URL changes the digest.
	elsewhere := heterogeneousSite(t)
	elsewherePlan, err := BuildPlan(context.Background(), reader, elsewhere.prober(), elsewhere.baseURL(), heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() on other base URL error = %v", err)
	}
	if elsewherePlan.Digest == plan.Digest {
		t.Fatal("a changed public base URL produced the approved digest")
	}
}

func TestPlanRejectsUnexpectedStorageObjects(t *testing.T) {
	site := heterogeneousSite(t)

	objects := heterogeneousObjects()
	objects["stray/abandoned.html"] = s3test.Object{
		Content: []byte("<html>stray</html>"), ContentType: "text/html",
		LastModified: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	_, strayReader := fakeBucket(t, objects)
	_, err := buildPlan(t, strayReader, site, heterogeneousDeclaration())
	if err == nil {
		t.Fatal("an undeclared storage object should reject the plan")
	}

	// An object that appears beneath a declared publication path but is not
	// declared is also unexpected.
	objects = heterogeneousObjects()
	objects["guides/undeclared.js"] = s3test.Object{
		Content: []byte("console.log('surprise')"), ContentType: "text/javascript",
		LastModified: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	_, undeclaredReader := fakeBucket(t, objects)
	if _, err := buildPlan(t, undeclaredReader, site, heterogeneousDeclaration()); err == nil {
		t.Fatal("an undeclared object beneath a publication path should reject the plan")
	}
}

func TestPlanRejectsMissingDeclaredObject(t *testing.T) {
	objects := heterogeneousObjects()
	delete(objects, "guides/assets/style.css")
	_, reader := fakeBucket(t, objects)
	site := heterogeneousSite(t)

	if _, err := buildPlan(t, reader, site, heterogeneousDeclaration()); err == nil {
		t.Fatal("planning a missing declared object should fail")
	}
}

func TestCommitAcceptsWholeBatch(t *testing.T) {
	bucket, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	operationID := catalog.NewID()
	result, err := Commit(context.Background(), CommitInput{
		Reader:      reader,
		Prober:      site.prober(),
		Approved:    plan,
		OperationID: operationID,
		CatalogPath: catalogPath,
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result.Replayed {
		t.Fatal("a fresh commit must not be a replay")
	}
	if result.Result.Status != "committed" || result.Result.ObjectCount != 5 {
		t.Fatalf("result = %+v", result.Result)
	}
	if len(result.Result.Projects) != 3 {
		t.Fatalf("result projects = %+v", result.Result.Projects)
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 3 {
		t.Fatalf("inventory has %d projects, want 3", len(inventory))
	}
	byPrefix := map[string]int{}
	for index, project := range inventory {
		byPrefix[project.Prefix] = index
	}
	guides := inventory[byPrefix["guides"]]
	if len(guides.Publications) != 1 || guides.Publications[0].Path != "guides" || guides.Publications[0].RoutingMode != "fallback" {
		t.Fatalf("guides project = %+v", guides)
	}
	reports := inventory[byPrefix["reports"]]
	if len(reports.Publications) != 1 || reports.Publications[0].EntryPoint != "reports/Index.html" {
		t.Fatalf("reports project = %+v", reports)
	}
	docs := inventory[byPrefix["docs"]]
	if len(docs.Publications) != 1 || docs.Publications[0].Path != "docs" {
		t.Fatalf("docs project = %+v", docs)
	}

	assertOnlyReadRequests(t, bucket)
	for _, request := range site.requests() {
		if request.method != http.MethodGet {
			t.Fatalf("probe request %s %s is not a read", request.method, request.path)
		}
	}
}

func TestAdoptionPreservesLegacyFacts(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}

	var caseSensitive, exact, fallback bool
	for _, project := range inventory {
		for _, publication := range project.Publications {
			switch publication.Path {
			case "reports":
				caseSensitive = true
				if publication.EntryPoint != "reports/Index.html" {
					t.Fatalf("entry point case not preserved: %+v", publication)
				}
				if publication.RoutingMode != "directory_index" {
					t.Fatalf("routing mode not preserved: %+v", publication)
				}
				if publication.ContentChangedAt != "2026-02-01T00:00:00Z" {
					t.Fatalf("content changed not preserved: %+v", publication)
				}
			case "docs":
				exact = true
				if publication.EntryPoint != "docs" || publication.RoutingMode != "exact_file" {
					t.Fatalf("exact-file publication not preserved: %+v", publication)
				}
			case "guides":
				fallback = true
				if publication.RoutingMode != "fallback" || publication.ContentChangedAt != "2026-01-02T03:04:05Z" {
					t.Fatalf("fallback publication not preserved: %+v", publication)
				}
			}
		}
	}
	if !caseSensitive || !exact || !fallback {
		t.Fatalf("missing publications in inventory: %+v", inventory)
	}
}

func TestCommitReplaysIdenticalOperation(t *testing.T) {
	bucket, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	operationID := catalog.NewID()
	first, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: operationID, CatalogPath: catalogPath,
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	requestsBeforeReplay := len(bucket.Requests())
	replay, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: operationID, CatalogPath: catalogPath,
	})
	if err != nil {
		t.Fatalf("Commit() replay error = %v", err)
	}
	if !replay.Replayed {
		t.Fatal("reused operation ID should return the durable result")
	}
	if replay.Result.Status != first.Result.Status || !reflect.DeepEqual(replay.Result, first.Result) {
		t.Fatalf("replay result %+v differs from original %+v", replay.Result, first.Result)
	}
	if len(bucket.Requests()) != requestsBeforeReplay {
		t.Fatal("a replay must not touch storage")
	}

	// Same operation ID with a different, valid plan fails.
	changed := heterogeneousObjects()
	changed["guides/index.html"] = s3test.Object{
		Content:      []byte("<html><body>rewritten</body></html>"),
		ContentType:  "text/html; charset=utf-8",
		LastModified: time.Date(2026, 3, 3, 3, 4, 5, 0, time.UTC),
		ETag:         `"etag-guides-index-v2"`,
	}
	_, changedReader := fakeBucket(t, changed)
	otherPlan, err := buildPlan(t, changedReader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if otherPlan.Digest == plan.Digest {
		t.Fatal("expected a different plan digest")
	}
	if _, err := Commit(context.Background(), CommitInput{
		Reader: changedReader, Prober: site.prober(), Approved: otherPlan,
		OperationID: operationID, CatalogPath: catalogPath,
	}); err == nil {
		t.Fatal("operation ID reuse with different content should fail")
	}
}

func TestCommitRejectsDriftedStorage(t *testing.T) {
	bucket, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Storage changes between approval and commit.
	bucket.SetObject("guides/index.html", s3test.Object{
		Content:      []byte("<html><body>changed</body></html>"),
		ContentType:  "text/html; charset=utf-8",
		LastModified: time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC),
		ETag:         `"etag-guides-index"`,
	})

	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err == nil {
		t.Fatal("committing drifted storage should fail")
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 0 {
		t.Fatalf("a rejected commit left %d projects behind", len(inventory))
	}
}

func TestCommitRejectsChangedProbeResults(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// The public route changes between approval and commit: the recheck
	// observes different bytes and must reject the complete batch.
	site.setRoute("/docs", siteResponse{status: http.StatusOK, contentType: "text/plain", body: []byte("repaired body")})

	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err == nil {
		t.Fatal("committing after a changed public probe should fail")
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 0 {
		t.Fatalf("a rejected commit left %d projects behind", len(inventory))
	}
}

func TestCommitRejectsAlreadyManagedPaths(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}

	// A different operation ID for the same already-managed state fails
	// closed instead of duplicating catalog records.
	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err == nil {
		t.Fatal("committing an already managed batch with a new operation ID should fail")
	}
}

func TestCommitRejectsPublicBaseURLMismatch(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Committing against a different origin than the plan was generated for
	// fails closed before touching the catalog.
	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID:           catalog.NewID(),
		CatalogPath:           catalogPath,
		ExpectedPublicBaseURL: "https://elsewhere.example",
	}); err == nil {
		t.Fatal("commit with a mismatched public base URL should fail")
	}

	store, err := catalog.OpenRuntime(catalogPath)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	defer store.Close()
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(inventory) != 0 {
		t.Fatalf("a rejected commit left %d projects behind", len(inventory))
	}
}

func TestCommitRefusesWhileRuntimeHoldsCatalog(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := migratedCatalog(t)

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Simulate the running manager holding the catalog.
	lock, err := catalog.AcquireLock(catalogPath)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	defer lock.Release()

	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err == nil {
		t.Fatal("commit must refuse while the runtime holds the catalog")
	}
}

func TestCommitRefusesUnmigratedCatalog(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)
	catalogPath := filepath.Join(t.TempDir(), "fresh.db")

	plan, err := buildPlan(t, reader, site, heterogeneousDeclaration())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := Commit(context.Background(), CommitInput{
		Reader: reader, Prober: site.prober(), Approved: plan,
		OperationID: catalog.NewID(), CatalogPath: catalogPath,
	}); err == nil {
		t.Fatal("commit must refuse an unmigrated catalog")
	}
}

func TestPlanRejectsInvalidPublicBaseURL(t *testing.T) {
	_, reader := fakeBucket(t, heterogeneousObjects())
	site := heterogeneousSite(t)

	cases := map[string]string{
		"empty":         "",
		"relative":      "share.bdgn.me",
		"not http":      "ftp://share.bdgn.me",
		"with query":    "https://share.bdgn.me/?x=1",
		"with fragment": "https://share.bdgn.me/#frag",
	}
	for name, baseURL := range cases {
		if _, err := BuildPlan(context.Background(), reader, site.prober(), baseURL, heterogeneousDeclaration()); err == nil {
			t.Errorf("%s: invalid public base URL should reject the plan", name)
		}
	}
}

// fakeSite is an in-process HTTP server standing in for the public
// share.bdgn.me routing layer. It records every request.
type fakeSite struct {
	server *httptest.Server

	mu      sync.Mutex
	routes  map[string]siteResponse
	history []siteRequest
	closed  bool
}

type siteResponse struct {
	status      int
	contentType string
	body        []byte
}

type siteRequest struct {
	method string
	path   string
}

func newFakeSite(t *testing.T) *fakeSite {
	t.Helper()
	site := &fakeSite{routes: map[string]siteResponse{}}
	site.server = httptest.NewServer(http.HandlerFunc(site.serve))
	t.Cleanup(site.close)
	return site
}

func (s *fakeSite) setRoute(path string, response siteResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = response
}

// baseURL is this site's origin, standing in for the canonical public base
// URL in tests.
func (s *fakeSite) baseURL() string { return s.server.URL }

func (s *fakeSite) requests() []siteRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]siteRequest(nil), s.history...)
}

func (s *fakeSite) close() {
	s.mu.Lock()
	closed := s.closed
	s.closed = true
	s.mu.Unlock()
	if !closed {
		s.server.Close()
	}
}

func (s *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.history = append(s.history, siteRequest{method: r.Method, path: r.URL.Path})
	response, ok := s.routes[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", response.contentType)
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body)
}

// prober returns a RouteProber pointed at this site.
func (s *fakeSite) prober() RouteProber {
	return NewHTTPRouteProberWithClient(s.server.Client())
}

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
	fixtures := map[string]fixtureEnvironment{
		"exact-file-uppercase.json": {
			objects: map[string]s3test.Object{
				"docs/Annual-Report.TXT": {
					Content:      []byte("Annual report"),
					ContentType:  "text/plain",
					LastModified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			routes: map[string]siteResponse{
				"/docs/Annual-Report.TXT": {status: http.StatusOK, contentType: "text/plain", body: []byte("Annual report")},
			},
		},
		"single-directory-index.json": {
			objects: map[string]s3test.Object{
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
			routes: map[string]siteResponse{
				"/guides/getting-started":           {status: http.StatusOK, contentType: "text/html", body: []byte("<html>start</html>")},
				"/guides/getting-started/deep/link": {status: http.StatusNotFound, contentType: "text/plain", body: []byte("not found")},
			},
		},
		"heterogeneous-batch.json": {
			objects: map[string]s3test.Object{
				"guides/index.html": {
					Content:      []byte("<html><body>guides home</body></html>"),
					ContentType:  "text/html; charset=utf-8",
					UserMetadata: map[string]string{"source": "legacy-share"},
					LastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
					ETag:         `"etag-guides-index"`,
				},
				"guides/assets/style.css": {
					Content:      []byte("body { color: black }"),
					ContentType:  "text/css",
					LastModified: time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC),
					ETag:         `"etag-style"`,
				},
				"reports/Index.html": {
					Content:      []byte("<html><body>Reports</body></html>"),
					ContentType:  "text/html",
					LastModified: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
					ETag:         `"etag-reports-index"`,
				},
				"reports/app.js": {
					Content:      []byte("console.log('ready')"),
					ContentType:  "text/javascript",
					LastModified: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
					ETag:         `"etag-app"`,
				},
				"docs": {
					Content:      []byte("Annual report body"),
					ContentType:  "text/plain",
					LastModified: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
					ETag:         `"etag-docs"`,
				},
			},
			routes: map[string]siteResponse{
				"/guides":            {status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>guides home</body></html>")},
				"/guides/anything":   {status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>guides home</body></html>")},
				"/reports":           {status: http.StatusOK, contentType: "text/html", body: []byte("<html><body>Reports</body></html>")},
				"/reports/deep/link": {status: http.StatusNotFound, contentType: "text/plain", body: []byte("not found")},
				"/docs":              {status: http.StatusOK, contentType: "text/plain", body: []byte("Annual report body")},
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
			environment, known := fixtures[entry.Name()]
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
			bucket := s3test.NewServer("page-hub", environment.objects)
			defer bucket.Close()
			reader := storage.NewS3Reader(storage.Config{
				Endpoint:        bucket.URL(),
				Bucket:          "page-hub",
				AccessKeyID:     "test-access-key",
				SecretAccessKey: "test-secret-key",
			})
			site := newFakeSite(t)
			for path, response := range environment.routes {
				site.setRoute(path, response)
			}
			plan, err := BuildPlan(context.Background(), reader, site.prober(), site.baseURL(), declaration)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if err := plan.Verify(); err != nil {
				t.Fatalf("plan digest does not verify: %v", err)
			}
		})
	}
}

// fixtureEnvironment is the storage and public-route state one fixture plans
// against.
type fixtureEnvironment struct {
	objects map[string]s3test.Object
	routes  map[string]siteResponse
}
