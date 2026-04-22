-- 004.up.sql — feature 018 schema v3 → v4
-- Adds revoke/edit columns on messages + idempotency_keys sidecar
-- (distinct from v3's message_idempotency which is message-scoped) +
-- migration_history. FR-013 + FR-015a + FR-034a.

-- Revoke/edit metadata on messages. ALTER TABLE ADD COLUMN is O(1) in
-- SQLite; no data rewrite. Columns are nullable sentinels by design.
ALTER TABLE messages ADD COLUMN revoked_at INTEGER;
ALTER TABLE messages ADD COLUMN edited_at INTEGER;
ALTER TABLE messages ADD COLUMN previous_body TEXT;
ALTER TABLE messages ADD COLUMN edit_of TEXT;

-- Index for thread.get to filter out revoked rows efficiently.
CREATE INDEX IF NOT EXISTS idx_messages_chat_revoked
    ON messages (chat_jid, revoked_at);

-- Idempotency-key sidecar (FR-034a). params_hash is SHA-256 raw bytes
-- over canonical-JSON params; mismatched hash on replay of same key
-- yields -32101 idempotency_collision.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    method        TEXT    NOT NULL,
    profile       TEXT    NOT NULL,
    key           TEXT    NOT NULL,
    params_hash   BLOB    NOT NULL,
    response_json BLOB    NOT NULL,
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    PRIMARY KEY (method, profile, key)
);

CREATE INDEX IF NOT EXISTS idx_idempotency_expires
    ON idempotency_keys (expires_at);

-- Migration history (FR-013).
CREATE TABLE IF NOT EXISTS migration_history (
    version     INTEGER NOT NULL,
    direction   TEXT    NOT NULL CHECK (direction IN ('up','down')),
    applied_at  INTEGER NOT NULL,
    backup_path TEXT    NOT NULL
);

-- Record this migration's own run. backup_path left empty here; the
-- application layer writes its own row with the real backup path
-- immediately after this SQL succeeds so the application knows which
-- snapshot the migration was taken against.
INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (4, 'up', CAST(strftime('%s','now') AS INTEGER), '');
