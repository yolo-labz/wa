-- sqlitedrafts schema v1. Embedded via go:embed. Idempotent.

CREATE TABLE IF NOT EXISTS drafts (
    id           TEXT PRIMARY KEY,
    profile      TEXT NOT NULL,
    kind         INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    target_jid   TEXT NOT NULL DEFAULT '',
    state        INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    decided_at   INTEGER NOT NULL DEFAULT 0,
    decided_by   TEXT NOT NULL DEFAULT '',
    reason       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_drafts_state_expiry ON drafts (profile, state, expires_at);
CREATE INDEX IF NOT EXISTS idx_drafts_target ON drafts (target_jid);

PRAGMA user_version = 1;
