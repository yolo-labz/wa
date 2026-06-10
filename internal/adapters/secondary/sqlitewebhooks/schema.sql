-- sqlitewebhooks schema v1 (feature 112). Idempotent.

CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id             TEXT NOT NULL,
    profile        TEXT NOT NULL,
    url            TEXT NOT NULL,
    secret         TEXT NOT NULL,
    topics         TEXT NOT NULL,
    disabled       INTEGER NOT NULL DEFAULT 0,
    disable_reason TEXT NOT NULL DEFAULT '',
    failure_streak INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (profile, id)
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id              TEXT NOT NULL,
    profile         TEXT NOT NULL,
    endpoint_id     TEXT NOT NULL,
    topic           TEXT NOT NULL,
    payload         TEXT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (profile, id)
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due
    ON webhook_deliveries (profile, state, next_attempt_at);

PRAGMA user_version = 1;
