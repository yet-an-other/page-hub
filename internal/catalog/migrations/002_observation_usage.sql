-- Page Hub observation usage columns.
--
-- Bucket observations record complete usage and the storage-mutation lock the
-- observation implies. These are first-class columns rather than details JSON
-- so the manager can report exact values without parsing blobs. Unclaimed
-- object keys stay in details_json.

ALTER TABLE observations ADD COLUMN mutation_lock TEXT NOT NULL DEFAULT 'none'
    CHECK (mutation_lock IN ('none', 'global', 'publication'));
ALTER TABLE observations ADD COLUMN total_bytes INTEGER NOT NULL DEFAULT 0
    CHECK (total_bytes >= 0);
ALTER TABLE observations ADD COLUMN accepted_bytes INTEGER NOT NULL DEFAULT 0
    CHECK (accepted_bytes >= 0);
ALTER TABLE observations ADD COLUMN unclaimed_bytes INTEGER NOT NULL DEFAULT 0
    CHECK (unclaimed_bytes >= 0);
