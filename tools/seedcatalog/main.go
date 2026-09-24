// Command seedcatalog creates a hermetic catalog fixture for browser
// acceptance tests. It migrates the catalog and accepts a deterministic
// adoption batch directly through the catalog package, then records one
// stale, complete bucket observation covering in-sync, drifted, and missing
// Publications plus unclaimed storage. It is test tooling and is never part
// of the release binary.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

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

	result, err := store.CommitAdoptionBatch(catalog.CommitAdoptionBatchInput{
		OperationID: "00000000-0000-4000-8000-000000000001",
		RequestHash: "seed-fixture",
		PlanDigest:  "sha256:seed-fixture",
		Projects: []catalog.AdoptedProject{
			{
				Prefix:      "notes",
				DisplayName: "Notes",
				Description: "Adopted project notes",
				Publications: []catalog.AdoptedPublication{
					{
						Path:             "notes/2026-report",
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
					{
						Path:             "notes/quarter-recap",
						DisplayName:      "Quarter Recap",
						Description:      "Quarterly summary deck",
						EntryPoint:       "notes/quarter-recap/index.html",
						RoutingMode:      "directory_index",
						ContentChangedAt: "2026-02-10T09:30:00Z",
						Objects: []catalog.AdoptedObject{
							{
								Key: "notes/quarter-recap/index.html", RelativePath: "index.html",
								Size: 150, ETag: `"seed-etag-recap"`, ModifiedAt: "2026-02-10T09:30:00Z",
								ContentType: "text/html", SHA256: "3333333333333333333333333333333333333333333333333333333333333333",
							},
						},
					},
				},
			},
			{
				Prefix:      "archive",
				DisplayName: "Archive",
				Publications: []catalog.AdoptedPublication{
					{
						Path:             "archive/legacy-site",
						DisplayName:      "Legacy Site",
						EntryPoint:       "archive/legacy-site/index.html",
						RoutingMode:      "fallback",
						ContentChangedAt: "2025-06-01T12:00:00Z",
						Objects: []catalog.AdoptedObject{
							{
								Key: "archive/legacy-site/index.html", RelativePath: "index.html",
								Size: 100, ETag: `"seed-etag-legacy"`, ModifiedAt: "2025-06-01T12:00:00Z",
								ContentType: "text/html", SHA256: "4444444444444444444444444444444444444444444444444444444444444444",
							},
						},
					},
					{
						Path:             "archive/snapshot",
						DisplayName:      "Snapshot",
						EntryPoint:       "archive/snapshot/Report.HTML",
						RoutingMode:      "exact_file",
						ContentChangedAt: "2025-11-20T08:00:00Z",
						Objects: []catalog.AdoptedObject{
							{
								Key: "archive/snapshot/Report.HTML", RelativePath: "Report.HTML",
								Size: 50, ETag: `"seed-etag-snapshot"`, ModifiedAt: "2025-11-20T08:00:00Z",
								ContentType: "text/html", SHA256: "5555555555555555555555555555555555555555555555555555555555555555",
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: %v\n", err)
		os.Exit(1)
	}

	// Adoption forbids empty Projects in this slice, but the inventory must
	// still render one (moves and deletions can empty a Project later), so
	// the fixture inserts an empty Project directly.
	db, err := sql.Open("sqlite", *catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: open catalog for empty project: %v\n", err)
		os.Exit(1)
	}
	seededAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO projects (id, prefix, display_name, description, entity_revision, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		catalog.NewID(), "empty", "Empty Shelf", "An empty project", "seed-revision", seededAt, seededAt,
	); err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: insert empty project: %v\n", err)
		os.Exit(1)
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: close catalog: %v\n", err)
		os.Exit(1)
	}

	publicationID := make(map[string]string)
	for _, project := range result.Projects {
		for _, publication := range project.Publications {
			publicationID[publication.PublicationPath] = publication.PublicationID
		}
	}
	inSyncReport := int64(150)
	driftedReport := int64(400)
	missingReport := int64(0)
	snapshotSize := int64(50)

	// One stale, complete observation: the timestamp is far in the past so
	// the manager reports staleness without a clock-dependent test.
	observedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := store.RecordObservation(catalog.ObservationRecord{
		ObservedAt:     observedAt,
		MutationLock:   catalog.LockNone,
		TotalBytes:     540,
		AcceptedBytes:  450,
		UnclaimedBytes: 90,
		UnclaimedKeys:  []string{"scratch/orphan.bin"},
		Publications: []catalog.PublicationObservationRecord{
			{PublicationID: publicationID["notes/2026-report"], State: catalog.StateInSync, ObservedSize: &inSyncReport},
			{
				PublicationID: publicationID["notes/quarter-recap"], State: catalog.StateDrifted,
				ObservedSize: &driftedReport, StatusDetail: "index.html content changed",
			},
			{
				PublicationID: publicationID["archive/legacy-site"], State: catalog.StateMissing,
				ObservedSize: &missingReport, StatusDetail: "no objects found under archive/legacy-site",
			},
			{PublicationID: publicationID["archive/snapshot"], State: catalog.StateInSync, ObservedSize: &snapshotSize},
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "seedcatalog: record observation: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("seeded", *catalogPath)
}
