-- Page Hub initial catalog schema.
--
-- The catalog, not the presence of objects in storage, defines managed
-- Publications (ADR-0001). This migration creates the state needed by the
-- first implementation slice: Projects, Publications, immutable accepted
-- manifest revisions, their exact object records, observations kept separate
-- from accepted state, and durable adoption operation results.

CREATE TABLE schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    checksum   TEXT NOT NULL,
    applied_at TEXT NOT NULL
);

CREATE TABLE projects (
    id              TEXT PRIMARY KEY CHECK (length(id) = 36),
    prefix          TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    entity_revision TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE publications (
    id                  TEXT PRIMARY KEY CHECK (length(id) = 36),
    project_id          TEXT NOT NULL REFERENCES projects(id),
    path                TEXT NOT NULL UNIQUE,
    display_name        TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    entry_point         TEXT NOT NULL,
    routing_mode        TEXT NOT NULL CHECK (routing_mode IN ('exact_file', 'directory_index', 'fallback')),
    content_changed_at  TEXT NOT NULL,
    entity_revision     TEXT NOT NULL,
    -- Deferred so a publication and its initial manifest revision can be
    -- created in either order inside one transaction.
    current_manifest_id TEXT REFERENCES manifest_revisions(id) DEFERRABLE INITIALLY DEFERRED,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE TABLE manifest_revisions (
    id             TEXT PRIMARY KEY CHECK (length(id) = 36),
    publication_id TEXT NOT NULL REFERENCES publications(id),
    plan_digest    TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE TABLE manifest_objects (
    manifest_id      TEXT NOT NULL REFERENCES manifest_revisions(id) ON DELETE CASCADE,
    object_key       TEXT NOT NULL,
    relative_path    TEXT NOT NULL,
    size             INTEGER NOT NULL CHECK (size >= 0),
    etag             TEXT NOT NULL,
    modified_at      TEXT NOT NULL,
    content_type     TEXT NOT NULL DEFAULT '',
    content_encoding TEXT NOT NULL DEFAULT '',
    cache_control    TEXT NOT NULL DEFAULT '',
    user_metadata    TEXT NOT NULL DEFAULT '{}',
    sha256           TEXT NOT NULL,
    PRIMARY KEY (manifest_id, object_key)
);

-- Observations record what storage looked like at a point in time. They never
-- change accepted state (ADR-0001).
CREATE TABLE observations (
    id           TEXT PRIMARY KEY CHECK (length(id) = 36),
    observed_at  TEXT NOT NULL,
    status       TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE publication_observations (
    id             TEXT PRIMARY KEY CHECK (length(id) = 36),
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    publication_id TEXT NOT NULL REFERENCES publications(id),
    state          TEXT NOT NULL CHECK (state IN ('in_sync', 'drifted', 'missing')),
    observed_size  INTEGER,
    status_detail  TEXT NOT NULL DEFAULT '',
    UNIQUE (observation_id, publication_id)
);

-- Durable adoption operation results. Reusing an operation ID with the same
-- request returns the recorded result; reuse with different content fails.
CREATE TABLE adoption_operations (
    operation_id TEXT PRIMARY KEY,
    request_hash TEXT NOT NULL,
    plan_digest  TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('committed')),
    result_json  TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

CREATE INDEX idx_publications_project ON publications(project_id);
CREATE INDEX idx_manifest_revisions_publication ON manifest_revisions(publication_id);
CREATE INDEX idx_publication_observations_publication ON publication_observations(publication_id);
