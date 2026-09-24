package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// AdoptedObject is one exact object record of an accepted manifest revision.
type AdoptedObject struct {
	Key             string            `json:"key"`
	RelativePath    string            `json:"relativePath"`
	Size            int64             `json:"size"`
	ETag            string            `json:"etag"`
	ModifiedAt      string            `json:"modifiedAt"`
	ContentType     string            `json:"contentType"`
	ContentEncoding string            `json:"contentEncoding"`
	CacheControl    string            `json:"cacheControl"`
	UserMetadata    map[string]string `json:"userMetadata"`
	SHA256          string            `json:"sha256"`
}

// AdoptedPublication is one declared Publication accepted into the catalog.
type AdoptedPublication struct {
	Path             string
	DisplayName      string
	Description      string
	EntryPoint       string
	RoutingMode      string
	ContentChangedAt string
	Objects          []AdoptedObject
}

// AdoptedProject is one declared Project accepted together with its
// Publications. Adoption never creates an empty Project.
type AdoptedProject struct {
	Prefix       string
	DisplayName  string
	Description  string
	Publications []AdoptedPublication
}

// CommitAdoptionBatchInput carries the durable operation identity plus the
// complete accepted batch committed in one transaction.
type CommitAdoptionBatchInput struct {
	OperationID string
	RequestHash string
	PlanDigest  string
	Projects    []AdoptedProject
}

// PublicationAdoptionResult is the durable result for one accepted
// Publication.
type PublicationAdoptionResult struct {
	PublicationID      string `json:"publicationId"`
	PublicationPath    string `json:"publicationPath"`
	ManifestRevisionID string `json:"manifestRevisionId"`
	ObjectCount        int    `json:"objectCount"`
	AcceptedSize       int64  `json:"acceptedSize"`
}

// ProjectAdoptionResult is the durable result for one accepted Project and
// its accepted Publications.
type ProjectAdoptionResult struct {
	ProjectID     string                      `json:"projectId"`
	ProjectPrefix string                      `json:"projectPrefix"`
	Publications  []PublicationAdoptionResult `json:"publications"`
}

// AdoptionResult is the durable result of an adoption operation. It is stored
// verbatim so an operation ID reuse returns the identical answer.
type AdoptionResult struct {
	OperationID  string                  `json:"operationId"`
	Status       string                  `json:"status"`
	ObjectCount  int                     `json:"objectCount"`
	AcceptedSize int64                   `json:"acceptedSize"`
	Projects     []ProjectAdoptionResult `json:"projects"`
	CommittedAt  string                  `json:"committedAt"`
}

// OperationRecord is the durable record of a previously committed operation.
type OperationRecord struct {
	OperationID string
	RequestHash string
	PlanDigest  string
	ResultJSON  string
}

// FindOperation returns the durable result recorded for an operation ID.
func (s *Store) FindOperation(operationID string) (OperationRecord, bool, error) {
	var record OperationRecord
	err := s.db.QueryRow(
		`SELECT operation_id, request_hash, plan_digest, result_json FROM adoption_operations WHERE operation_id = ?`,
		operationID,
	).Scan(&record.OperationID, &record.RequestHash, &record.PlanDigest, &record.ResultJSON)
	if err == sql.ErrNoRows {
		return OperationRecord{}, false, nil
	}
	if err != nil {
		return OperationRecord{}, false, fmt.Errorf("look up adoption operation: %w", err)
	}
	return record, true, nil
}

// CommitAdoptionBatch accepts every declared Project, Publication, and
// immutable initial manifest revision of one adoption batch, plus the adoption
// operation record, in exactly one transaction. Any failure accepts none of
// them. Preconditions are rechecked inside the transaction so a concurrent
// writer cannot slip past them.
func (s *Store) CommitAdoptionBatch(input CommitAdoptionBatchInput) (AdoptionResult, error) {
	if input.OperationID == "" {
		return AdoptionResult{}, fmt.Errorf("an operation ID is required")
	}
	projects, err := normalizeBatchProjects(input.Projects)
	if err != nil {
		return AdoptionResult{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("begin adoption transaction: %w", err)
	}
	defer tx.Rollback()

	var existing string
	for _, project := range projects {
		err := tx.QueryRow(`SELECT prefix FROM projects WHERE prefix = ?`, project.Prefix).Scan(&existing)
		if err == nil {
			return AdoptionResult{}, fmt.Errorf("project prefix %q is already managed", project.Prefix)
		}
		if err != sql.ErrNoRows {
			return AdoptionResult{}, fmt.Errorf("check project prefix: %w", err)
		}
		for _, publication := range project.Publications {
			err := tx.QueryRow(`SELECT path FROM publications WHERE path = ?`, publication.Path).Scan(&existing)
			if err == nil {
				return AdoptionResult{}, fmt.Errorf("publication path %q is already managed", publication.Path)
			}
			if err != sql.ErrNoRows {
				return AdoptionResult{}, fmt.Errorf("check publication path: %w", err)
			}
		}
	}

	err = tx.QueryRow(`SELECT operation_id FROM adoption_operations WHERE operation_id = ?`, input.OperationID).Scan(&existing)
	if err == nil {
		return AdoptionResult{}, fmt.Errorf("operation ID %q already exists with different content", input.OperationID)
	}
	if err != sql.ErrNoRows {
		return AdoptionResult{}, fmt.Errorf("check adoption operation: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result := AdoptionResult{
		OperationID: input.OperationID,
		Status:      "committed",
		Projects:    make([]ProjectAdoptionResult, 0, len(projects)),
	}

	for _, project := range projects {
		projectID := NewID()
		if _, err := tx.Exec(
			`INSERT INTO projects (id, prefix, display_name, description, entity_revision, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			projectID, project.Prefix, project.DisplayName, project.Description, NewID(), now, now,
		); err != nil {
			return AdoptionResult{}, fmt.Errorf("insert project %q: %w", project.Prefix, err)
		}

		projectResult := ProjectAdoptionResult{
			ProjectID:     projectID,
			ProjectPrefix: project.Prefix,
			Publications:  make([]PublicationAdoptionResult, 0, len(project.Publications)),
		}
		for _, publication := range project.Publications {
			publicationID, manifestID := NewID(), NewID()

			acceptedSize := int64(0)
			for _, object := range publication.Objects {
				acceptedSize += object.Size
			}

			if _, err := tx.Exec(
				`INSERT INTO publications (id, project_id, path, display_name, description, entry_point, routing_mode,
				 content_changed_at, entity_revision, current_manifest_id, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				publicationID, projectID, publication.Path, publication.DisplayName, publication.Description,
				publication.EntryPoint, publication.RoutingMode, publication.ContentChangedAt, NewID(), manifestID, now, now,
			); err != nil {
				return AdoptionResult{}, fmt.Errorf("insert publication %q: %w", publication.Path, err)
			}

			if _, err := tx.Exec(
				`INSERT INTO manifest_revisions (id, publication_id, plan_digest, created_at) VALUES (?, ?, ?, ?)`,
				manifestID, publicationID, input.PlanDigest, now,
			); err != nil {
				return AdoptionResult{}, fmt.Errorf("insert manifest revision for %q: %w", publication.Path, err)
			}

			for _, object := range publication.Objects {
				metadata, err := json.Marshal(object.UserMetadata)
				if err != nil {
					return AdoptionResult{}, fmt.Errorf("encode user metadata: %w", err)
				}
				if _, err := tx.Exec(
					`INSERT INTO manifest_objects (manifest_id, object_key, relative_path, size, etag, modified_at,
					 content_type, content_encoding, cache_control, user_metadata, sha256)
					 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					manifestID, object.Key, object.RelativePath, object.Size, object.ETag, object.ModifiedAt,
					object.ContentType, object.ContentEncoding, object.CacheControl, string(metadata), object.SHA256,
				); err != nil {
					return AdoptionResult{}, fmt.Errorf("insert manifest object %q: %w", object.Key, err)
				}
			}

			projectResult.Publications = append(projectResult.Publications, PublicationAdoptionResult{
				PublicationID:      publicationID,
				PublicationPath:    publication.Path,
				ManifestRevisionID: manifestID,
				ObjectCount:        len(publication.Objects),
				AcceptedSize:       acceptedSize,
			})
			result.ObjectCount += len(publication.Objects)
			result.AcceptedSize += acceptedSize
		}
		result.Projects = append(result.Projects, projectResult)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("encode adoption result: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO adoption_operations (operation_id, request_hash, plan_digest, status, result_json, created_at)
		 VALUES (?, ?, ?, 'committed', ?, ?)`,
		input.OperationID, input.RequestHash, input.PlanDigest, string(encoded), now,
	); err != nil {
		return AdoptionResult{}, fmt.Errorf("record adoption operation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AdoptionResult{}, fmt.Errorf("commit adoption transaction: %w", err)
	}
	return result, nil
}

// normalizeBatchProjects copies the batch, rejects incomplete input, and
// orders projects and publications deterministically so results are stable.
func normalizeBatchProjects(projects []AdoptedProject) ([]AdoptedProject, error) {
	if len(projects) == 0 {
		return nil, fmt.Errorf("an adoption batch must contain at least one project")
	}
	seenPrefixes := map[string]bool{}
	seenPaths := map[string]bool{}
	normalized := make([]AdoptedProject, 0, len(projects))
	for _, project := range projects {
		if project.Prefix == "" {
			return nil, fmt.Errorf("every adopted project needs a prefix")
		}
		if len(project.Publications) == 0 {
			return nil, fmt.Errorf("adopted project %q has no publications", project.Prefix)
		}
		if seenPrefixes[project.Prefix] {
			return nil, fmt.Errorf("project prefix %q is declared twice in the batch", project.Prefix)
		}
		seenPrefixes[project.Prefix] = true

		publications := make([]AdoptedPublication, len(project.Publications))
		copy(publications, project.Publications)
		sort.Slice(publications, func(i, j int) bool { return publications[i].Path < publications[j].Path })
		for _, publication := range publications {
			if publication.Path == "" {
				return nil, fmt.Errorf("every adopted publication of project %q needs a path", project.Prefix)
			}
			if seenPaths[publication.Path] {
				return nil, fmt.Errorf("publication path %q is declared twice in the batch", publication.Path)
			}
			seenPaths[publication.Path] = true
			if len(publication.Objects) == 0 {
				return nil, fmt.Errorf("adopted publication %q has no objects", publication.Path)
			}
		}
		project.Publications = publications
		normalized = append(normalized, project)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Prefix < normalized[j].Prefix })
	return normalized, nil
}
