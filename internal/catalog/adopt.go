package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

// AdoptPublication is one declared Publication accepted into the catalog.
type AdoptPublication struct {
	ProjectPrefix    string
	ProjectDisplay   string
	PublicationPath  string
	DisplayName      string
	Description      string
	EntryPoint       string
	RoutingMode      string
	ContentChangedAt string
	Objects          []AdoptedObject
}

// CommitAdoptionInput carries the durable operation identity plus the accepted
// publication state committed in one transaction.
type CommitAdoptionInput struct {
	OperationID string
	RequestHash string
	PlanDigest  string
	Publication AdoptPublication
}

// AdoptionResult is the durable result of an adoption operation. It is stored
// verbatim so an operation ID reuse returns the identical answer.
type AdoptionResult struct {
	OperationID        string `json:"operationId"`
	Status             string `json:"status"`
	ProjectID          string `json:"projectId"`
	ProjectPrefix      string `json:"projectPrefix"`
	PublicationID      string `json:"publicationId"`
	PublicationPath    string `json:"publicationPath"`
	ManifestRevisionID string `json:"manifestRevisionId"`
	ObjectCount        int    `json:"objectCount"`
	AcceptedSize       int64  `json:"acceptedSize"`
	CommittedAt        string `json:"committedAt"`
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

// CommitAdoption accepts one declared Publication, its Project, its immutable
// initial manifest revision, and the adoption operation record in exactly one
// transaction. Any failure accepts none of them. Preconditions are rechecked
// inside the transaction so a concurrent writer cannot slip past them.
func (s *Store) CommitAdoption(input CommitAdoptionInput) (AdoptionResult, error) {
	publication := input.Publication
	if publication.ProjectPrefix == "" || publication.PublicationPath == "" || len(publication.Objects) == 0 {
		return AdoptionResult{}, fmt.Errorf("incomplete adoption input")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("begin adoption transaction: %w", err)
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRow(`SELECT prefix FROM projects WHERE prefix = ?`, publication.ProjectPrefix).Scan(&existing)
	if err == nil {
		return AdoptionResult{}, fmt.Errorf("project prefix %q is already managed", publication.ProjectPrefix)
	}
	if err != sql.ErrNoRows {
		return AdoptionResult{}, fmt.Errorf("check project prefix: %w", err)
	}

	err = tx.QueryRow(`SELECT path FROM publications WHERE path = ?`, publication.PublicationPath).Scan(&existing)
	if err == nil {
		return AdoptionResult{}, fmt.Errorf("publication path %q is already managed", publication.PublicationPath)
	}
	if err != sql.ErrNoRows {
		return AdoptionResult{}, fmt.Errorf("check publication path: %w", err)
	}

	err = tx.QueryRow(`SELECT operation_id FROM adoption_operations WHERE operation_id = ?`, input.OperationID).Scan(&existing)
	if err == nil {
		return AdoptionResult{}, fmt.Errorf("operation ID %q already exists with different content", input.OperationID)
	}
	if err != sql.ErrNoRows {
		return AdoptionResult{}, fmt.Errorf("check adoption operation: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	projectID, publicationID, manifestID := NewID(), NewID(), NewID()

	if _, err := tx.Exec(
		`INSERT INTO projects (id, prefix, display_name, description, entity_revision, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, publication.ProjectPrefix, publication.ProjectDisplay, publication.Description, NewID(), now, now,
	); err != nil {
		return AdoptionResult{}, fmt.Errorf("insert project: %w", err)
	}

	acceptedSize := int64(0)
	for _, object := range publication.Objects {
		acceptedSize += object.Size
	}

	if _, err := tx.Exec(
		`INSERT INTO publications (id, project_id, path, display_name, description, entry_point, routing_mode,
		 content_changed_at, entity_revision, current_manifest_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		publicationID, projectID, publication.PublicationPath, publication.DisplayName, publication.Description,
		publication.EntryPoint, publication.RoutingMode, publication.ContentChangedAt, NewID(), manifestID, now, now,
	); err != nil {
		return AdoptionResult{}, fmt.Errorf("insert publication: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO manifest_revisions (id, publication_id, plan_digest, created_at) VALUES (?, ?, ?, ?)`,
		manifestID, publicationID, input.PlanDigest, now,
	); err != nil {
		return AdoptionResult{}, fmt.Errorf("insert manifest revision: %w", err)
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
			return AdoptionResult{}, fmt.Errorf("insert manifest object: %w", err)
		}
	}

	result := AdoptionResult{
		OperationID:        input.OperationID,
		Status:             "committed",
		ProjectID:          projectID,
		ProjectPrefix:      publication.ProjectPrefix,
		PublicationID:      publicationID,
		PublicationPath:    publication.PublicationPath,
		ManifestRevisionID: manifestID,
		ObjectCount:        len(publication.Objects),
		AcceptedSize:       acceptedSize,
		CommittedAt:        now,
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
