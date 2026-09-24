package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Observation states for a managed Publication. They describe observed
// storage and never change accepted state (ADR-0001).
const (
	StateInSync  = "in_sync"
	StateDrifted = "drifted"
	StateMissing = "missing"
)

// Storage-mutation locks recorded with an observation. Any unclassified
// out-of-band finding would place later storage mutations under a global
// lock; drift attributable to a known Page Hub operation would remain
// publication-specific. This slice records the state but implements no
// mutations (see the cutover contract).
const (
	LockNone            = "none"
	LockGlobal          = "global"
	LockPublication     = "publication"
	observationComplete = "complete"
)

// ManifestObject is the accepted state of one object in the current
// manifest revision of a Publication.
type ManifestObject struct {
	Key             string
	RelativePath    string
	Size            int64
	ETag            string
	ModifiedAt      string
	ContentType     string
	ContentEncoding string
	CacheControl    string
	UserMetadata    map[string]string
	SHA256          string
}

// PublicationManifest is the accepted manifest of one managed Publication.
type PublicationManifest struct {
	PublicationID string
	Path          string
	Objects       []ManifestObject
}

// AcceptedManifests returns the current accepted manifest of every managed
// Publication.
func (s *Store) AcceptedManifests(ctx context.Context) ([]PublicationManifest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pub.id, pub.path,
		       o.object_key, o.relative_path, o.size, o.etag, o.modified_at,
		       o.content_type, o.content_encoding, o.cache_control, o.user_metadata, o.sha256
		FROM publications pub
		JOIN manifest_objects o ON o.manifest_id = pub.current_manifest_id
		ORDER BY pub.path, o.object_key`)
	if err != nil {
		return nil, fmt.Errorf("query accepted manifests: %w", err)
	}
	defer rows.Close()

	manifests := map[string]*PublicationManifest{}
	var order []string
	for rows.Next() {
		var publicationID, path string
		var object ManifestObject
		var metadata string
		if err := rows.Scan(
			&publicationID, &path,
			&object.Key, &object.RelativePath, &object.Size, &object.ETag, &object.ModifiedAt,
			&object.ContentType, &object.ContentEncoding, &object.CacheControl, &metadata, &object.SHA256,
		); err != nil {
			return nil, fmt.Errorf("scan accepted manifest row: %w", err)
		}
		if err := json.Unmarshal([]byte(metadata), &object.UserMetadata); err != nil {
			return nil, fmt.Errorf("decode accepted metadata for %q: %w", object.Key, err)
		}
		manifest, seen := manifests[publicationID]
		if !seen {
			manifest = &PublicationManifest{PublicationID: publicationID, Path: path}
			manifests[publicationID] = manifest
			order = append(order, publicationID)
		}
		manifest.Objects = append(manifest.Objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accepted manifests: %w", err)
	}

	result := make([]PublicationManifest, 0, len(order))
	for _, id := range order {
		result = append(result, *manifests[id])
	}
	return result, nil
}

// ObservationRecord is one complete bucket observation to persist together
// with the per-Publication comparison results.
type ObservationRecord struct {
	ObservedAt     time.Time
	MutationLock   string
	TotalBytes     int64
	AcceptedBytes  int64
	UnclaimedBytes int64
	UnclaimedKeys  []string
	Publications   []PublicationObservationRecord
}

// PublicationObservationRecord is the comparison result for one managed
// Publication.
type PublicationObservationRecord struct {
	PublicationID string
	State         string
	ObservedSize  *int64
	StatusDetail  string
}

// RecordObservation persists one complete bucket observation and every
// per-Publication result in exactly one transaction.
func (s *Store) RecordObservation(record ObservationRecord) error {
	switch record.MutationLock {
	case LockNone, LockGlobal, LockPublication:
	default:
		return fmt.Errorf("invalid mutation lock %q", record.MutationLock)
	}
	if record.ObservedAt.IsZero() {
		return fmt.Errorf("an observation time is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin observation transaction: %w", err)
	}
	defer tx.Rollback()

	details, err := json.Marshal(map[string]any{"unclaimedKeys": record.UnclaimedKeys})
	if err != nil {
		return fmt.Errorf("encode observation details: %w", err)
	}
	observationID := NewID()
	if _, err := tx.Exec(
		`INSERT INTO observations (id, observed_at, status, details_json, mutation_lock, total_bytes, accepted_bytes, unclaimed_bytes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		observationID, record.ObservedAt.UTC().Format(time.RFC3339), observationComplete, string(details),
		record.MutationLock, record.TotalBytes, record.AcceptedBytes, record.UnclaimedBytes,
	); err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}

	for _, publication := range record.Publications {
		switch publication.State {
		case StateInSync, StateDrifted, StateMissing:
		default:
			return fmt.Errorf("invalid observed state %q for publication %q", publication.State, publication.PublicationID)
		}
		if _, err := tx.Exec(
			`INSERT INTO publication_observations (id, observation_id, publication_id, state, observed_size, status_detail)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			NewID(), observationID, publication.PublicationID, publication.State, publication.ObservedSize, publication.StatusDetail,
		); err != nil {
			return fmt.Errorf("insert observation for publication %q: %w", publication.PublicationID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit observation transaction: %w", err)
	}
	return nil
}

// BucketUsage is the exact usage of the complete bucket observation. Quota is
// deployment configuration; the manager API attaches it, the catalog does not
// store it.
type BucketUsage struct {
	QuotaBytes     int64 `json:"quotaBytes"`
	TotalBytes     int64 `json:"totalBytes"`
	AcceptedBytes  int64 `json:"acceptedBytes"`
	UnclaimedBytes int64 `json:"unclaimedBytes"`
}

// PublicationObservation is the observed state of one managed Publication.
type PublicationObservation struct {
	State        string `json:"state"`
	ObservedSize *int64 `json:"observedSize"`
	StatusDetail string `json:"statusDetail"`
}

// ObservationResult is the latest complete bucket observation together with
// its per-Publication results.
type ObservationResult struct {
	ObservationID string
	ObservedAt    time.Time
	MutationLock  string
	Usage         BucketUsage
	UnclaimedKeys []string
	Publications  map[string]PublicationObservation
}

// LatestObservation returns the newest complete bucket observation with its
// per-Publication results. found is false before the first successful scan.
func (s *Store) LatestObservation(ctx context.Context) (ObservationResult, bool, error) {
	result := ObservationResult{Publications: map[string]PublicationObservation{}}

	var observedAt, details string
	row := s.db.QueryRowContext(ctx, `
		SELECT id, observed_at, mutation_lock, total_bytes, accepted_bytes, unclaimed_bytes, details_json
		FROM observations
		WHERE status = ?
		ORDER BY observed_at DESC, rowid DESC
		LIMIT 1`, observationComplete)
	if err := row.Scan(
		&result.ObservationID, &observedAt, &result.MutationLock,
		&result.Usage.TotalBytes, &result.Usage.AcceptedBytes, &result.Usage.UnclaimedBytes,
		&details,
	); err != nil {
		if err == sql.ErrNoRows {
			return ObservationResult{Publications: map[string]PublicationObservation{}}, false, nil
		}
		return ObservationResult{}, false, fmt.Errorf("query latest observation: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, observedAt)
	if err != nil {
		return ObservationResult{}, false, fmt.Errorf("parse observation time: %w", err)
	}
	result.ObservedAt = parsed.UTC()

	var stored struct {
		UnclaimedKeys []string `json:"unclaimedKeys"`
	}
	if err := json.Unmarshal([]byte(details), &stored); err != nil {
		return ObservationResult{}, false, fmt.Errorf("decode observation details: %w", err)
	}
	result.UnclaimedKeys = stored.UnclaimedKeys

	rows, err := s.db.QueryContext(ctx, `
		SELECT publication_id, state, observed_size, status_detail
		FROM publication_observations
		WHERE observation_id = ?`, result.ObservationID)
	if err != nil {
		return ObservationResult{}, false, fmt.Errorf("query observation publications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var publicationID, state, statusDetail string
		var observedSize sql.NullInt64
		if err := rows.Scan(&publicationID, &state, &observedSize, &statusDetail); err != nil {
			return ObservationResult{}, false, fmt.Errorf("scan observation publication row: %w", err)
		}
		publication := PublicationObservation{State: state, StatusDetail: statusDetail}
		if observedSize.Valid {
			size := observedSize.Int64
			publication.ObservedSize = &size
		}
		result.Publications[publicationID] = publication
	}
	if err := rows.Err(); err != nil {
		return ObservationResult{}, false, fmt.Errorf("iterate observation publications: %w", err)
	}
	return result, true, nil
}
