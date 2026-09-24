package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PublicationPreview is the authenticated preview view of one managed
// Publication: its identity plus its state in the latest complete bucket
// observation. Observation is nil before the first successful scan, and a
// nil observation never means in sync.
type PublicationPreview struct {
	ID          string
	Path        string
	DisplayName string
	Observation *PublicationObservation
	// ObservedAt is the time of the observation carrying the state above.
	// It is the zero time when Observation is nil.
	ObservedAt time.Time
}

// PublicationPreview looks one managed Publication up by its exact path and
// returns its state in the latest complete bucket observation.
func (s *Store) PublicationPreview(ctx context.Context, publicationPath string) (PublicationPreview, bool, error) {
	var preview PublicationPreview
	var observationID sql.NullString
	var state, statusDetail, observedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT pub.id, pub.path, pub.display_name,
		       o.observation_id, obs.observed_at, o.state, o.status_detail
		FROM publications pub
		LEFT JOIN observations obs ON obs.id = (
			SELECT obs_latest.id
			FROM observations obs_latest
			WHERE obs_latest.status = 'complete'
			ORDER BY obs_latest.observed_at DESC, obs_latest.rowid DESC
			LIMIT 1
		)
		LEFT JOIN publication_observations o ON o.observation_id = obs.id AND o.publication_id = pub.id
		WHERE pub.path = ?`, publicationPath).Scan(
		&preview.ID, &preview.Path, &preview.DisplayName,
		&observationID, &observedAt, &state, &statusDetail,
	)
	if err == sql.ErrNoRows {
		return PublicationPreview{}, false, nil
	}
	if err != nil {
		return PublicationPreview{}, false, fmt.Errorf("query publication preview: %w", err)
	}

	if observationID.Valid {
		parsed, err := time.Parse(time.RFC3339, observedAt.String)
		if err != nil {
			return PublicationPreview{}, false, fmt.Errorf("parse preview observation time: %w", err)
		}
		state := state.String
		if state == "" {
			// The publication predates the latest observation or joined
			// after it; the catalog has no observed verdict for it yet.
			return preview, true, nil
		}
		preview.Observation = &PublicationObservation{
			State:        state,
			StatusDetail: statusDetail.String,
		}
		preview.ObservedAt = parsed.UTC()
	}
	return preview, true, nil
}
