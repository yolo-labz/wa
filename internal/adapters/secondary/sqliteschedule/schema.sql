-- sqliteschedule schema v1 (feature 017 / FR-111). Idempotent.

CREATE TABLE IF NOT EXISTS scheduled_sends (
    id          TEXT NOT NULL,
    profile     TEXT NOT NULL,
    kind        INTEGER NOT NULL,
    recipient   TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    media_path  TEXT NOT NULL DEFAULT '',
    fire_at     INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    state       INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    last_error  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (profile, id)
);

CREATE INDEX IF NOT EXISTS idx_scheduled_pending_fire_at
    ON scheduled_sends (profile, state, fire_at);

PRAGMA user_version = 1;
