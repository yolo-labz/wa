-- sqliteevents schema v1. Embedded via go:embed. Idempotent.

CREATE TABLE IF NOT EXISTS events (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,
    kind            TEXT NOT NULL,
    trusted_json    BLOB NOT NULL,
    untrusted_json  BLOB NOT NULL DEFAULT x'',
    created_at      INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_kind ON events (kind, seq);

CREATE TABLE IF NOT EXISTS events_meta (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    capacity       INTEGER NOT NULL,
    dropped_total  INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO events_meta (id, capacity, dropped_total) VALUES (1, 10000, 0);

PRAGMA user_version = 1;
