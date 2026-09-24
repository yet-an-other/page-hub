package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// InventoryPublication is the catalog-backed view of one managed Publication.
// Accepted facts come from the current manifest; Observation carries the
// latest storage comparison and never replaces accepted values.
type InventoryPublication struct {
	ID               string                  `json:"id"`
	Path             string                  `json:"path"`
	DisplayName      string                  `json:"displayName"`
	Description      string                  `json:"description"`
	EntryPoint       string                  `json:"entryPoint"`
	RoutingMode      string                  `json:"routingMode"`
	Size             int64                   `json:"size"`
	ContentChangedAt string                  `json:"contentChangedAt"`
	Observation      *PublicationObservation `json:"observation"`
}

// InventoryProject is the catalog-backed view of one managed Project.
type InventoryProject struct {
	ID           string                 `json:"id"`
	Prefix       string                 `json:"prefix"`
	DisplayName  string                 `json:"displayName"`
	Description  string                 `json:"description"`
	Publications []InventoryPublication `json:"publications"`
}

// BucketObservation is the manager-API view of the latest complete bucket
// observation. Usage.QuotaBytes stays zero here: the manager API attaches the
// configured quota.
type BucketObservation struct {
	ObservedAt   time.Time   `json:"observedAt"`
	MutationLock string      `json:"mutationLock"`
	Usage        BucketUsage `json:"usage"`
}

// Inventory is the catalog-backed manager view: every Project and Publication
// plus the latest bucket observation, kept separate from accepted state.
type Inventory struct {
	Projects    []InventoryProject `json:"projects"`
	Observation *BucketObservation `json:"observation"`
}

// Inventory returns every Project with every managed Publication and the
// latest bucket observation, sorted by display name without case sensitivity,
// using the exact path as tie-breaker. Empty Projects appear too. A missing
// observation leaves Observation nil.
func (s *Store) Inventory(ctx context.Context) (Inventory, error) {
	latest, found, err := s.LatestObservation(ctx)
	if err != nil {
		return Inventory{}, err
	}
	var publications map[string]PublicationObservation
	if found {
		publications = latest.Publications
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.prefix, p.display_name, p.description,
		       pub.id, pub.path, pub.display_name, pub.description, pub.entry_point, pub.routing_mode,
		       pub.content_changed_at,
		       COALESCE((SELECT SUM(o.size) FROM manifest_objects o WHERE o.manifest_id = pub.current_manifest_id), 0)
		FROM projects p
		LEFT JOIN publications pub ON pub.project_id = p.id
		ORDER BY p.prefix, pub.path`)
	if err != nil {
		return Inventory{}, fmt.Errorf("query inventory: %w", err)
	}
	defer rows.Close()

	projects := map[string]*InventoryProject{}
	var order []string
	for rows.Next() {
		var projectID, prefix, projectDisplay, projectDescription string
		var publication InventoryPublication
		var publicationID, publicationPath, publicationDisplay, publicationDescription, publicationEntryPoint, publicationRoutingMode, publicationChanged sql.NullString
		var publicationSize sql.NullInt64
		if err := rows.Scan(
			&projectID, &prefix, &projectDisplay, &projectDescription,
			&publicationID, &publicationPath, &publicationDisplay, &publicationDescription,
			&publicationEntryPoint, &publicationRoutingMode, &publicationChanged, &publicationSize,
		); err != nil {
			return Inventory{}, fmt.Errorf("scan inventory row: %w", err)
		}
		project, seen := projects[projectID]
		if !seen {
			project = &InventoryProject{
				ID:          projectID,
				Prefix:      prefix,
				DisplayName: projectDisplay,
				Description: projectDescription,
			}
			projects[projectID] = project
			order = append(order, projectID)
		}
		if publicationID.Valid {
			publication.ID = publicationID.String
			publication.Path = publicationPath.String
			publication.DisplayName = publicationDisplay.String
			publication.Description = publicationDescription.String
			publication.EntryPoint = publicationEntryPoint.String
			publication.RoutingMode = publicationRoutingMode.String
			publication.ContentChangedAt = publicationChanged.String
			publication.Size = publicationSize.Int64
			if found {
				if observed, ok := publications[publicationID.String]; ok {
					observedCopy := observed
					publication.Observation = &observedCopy
				}
			}
			project.Publications = append(project.Publications, publication)
		}
	}
	if err := rows.Err(); err != nil {
		return Inventory{}, fmt.Errorf("iterate inventory: %w", err)
	}

	inventory := Inventory{Projects: make([]InventoryProject, 0, len(order))}
	for _, id := range order {
		project := projects[id]
		sortPublications(project.Publications)
		inventory.Projects = append(inventory.Projects, *project)
	}
	sortProjects(inventory.Projects)
	if found {
		inventory.Observation = &BucketObservation{
			ObservedAt:   latest.ObservedAt,
			MutationLock: latest.MutationLock,
			Usage:        latest.Usage,
		}
	}
	return inventory, nil
}

func sortPublications(publications []InventoryPublication) {
	sort.SliceStable(publications, func(i, j int) bool {
		left, right := publications[i], publications[j]
		if order := strings.ToLower(left.DisplayName); order != strings.ToLower(right.DisplayName) {
			return order < strings.ToLower(right.DisplayName)
		}
		return left.Path < right.Path
	})
}

func sortProjects(projects []InventoryProject) {
	sort.SliceStable(projects, func(i, j int) bool {
		left, right := projects[i], projects[j]
		if order := strings.ToLower(left.DisplayName); order != strings.ToLower(right.DisplayName) {
			return order < strings.ToLower(right.DisplayName)
		}
		return left.Prefix < right.Prefix
	})
}
