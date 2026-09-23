// Command seedcatalog creates a hermetic catalog fixture for browser
// acceptance tests. It migrates the catalog and accepts one deterministic
// Project and Publication directly through the catalog package. It is test
// tooling and is never part of the release binary.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/yet-an-other/page-hub/internal/catalog"
)

func main() {
	catalogPath := flag.String("catalog", "", "path to the SQLite catalog to seed")
	fresh := flag.Bool("fresh", true, "remove any existing catalog before seeding")
	flag.Parse()
	if *catalogPath == "" {
		fmt.Fprintln(os.Stderr, "seedcatalog: -catalog <path> is required")
		os.Exit(2)
	}
	if *fresh {
		for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
			_ = os.Remove(*catalogPath + suffix)
		}
	}
	if _, _, err := catalog.Migrate(*catalogPath); err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: %v\n", err)
		os.Exit(1)
	}

	lock, err := catalog.AcquireLock(*catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: %v\n", err)
		os.Exit(1)
	}
	defer lock.Release()

	store, err := catalog.OpenRuntime(*catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	_, err = store.CommitAdoption(catalog.CommitAdoptionInput{
		OperationID: "00000000-0000-4000-8000-000000000001",
		RequestHash: "seed-fixture",
		PlanDigest:  "sha256:seed-fixture",
		Publication: catalog.AdoptedPublication{
			ProjectPrefix:    "notes",
			ProjectDisplay:   "Notes",
			Description:      "Adopted project notes",
			PublicationPath:  "notes/2026-report",
			DisplayName:      "2026 Report",
			EntryPoint:       "notes/2026-report/index.html",
			RoutingMode:      "directory_index",
			ContentChangedAt: "2026-01-02T03:04:05Z",
			Objects: []catalog.AdoptedObject{
				{
					Key: "notes/2026-report/index.html", RelativePath: "index.html",
					Size: 120, ETag: `"seed-etag-html"`, ModifiedAt: "2026-01-02T03:04:05Z",
					ContentType: "text/html", SHA256: "1111111111111111111111111111111111111111111111111111111111111111",
					UserMetadata: map[string]string{"source": "seed"},
				},
				{
					Key: "notes/2026-report/style.css", RelativePath: "style.css",
					Size: 30, ETag: `"seed-etag-css"`, ModifiedAt: "2025-12-31T10:00:00Z",
					ContentType: "text/css", SHA256: "2222222222222222222222222222222222222222222222222222222222222222",
				},
			},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("seeded", *catalogPath)
}
