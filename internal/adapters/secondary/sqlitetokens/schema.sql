-- spec 110d — REST adapter token store
-- One row per issued token. Raw token never persisted; only sha256.
-- Revocation is row-level (revoked_at IS NOT NULL); sweep can later
-- DELETE rows whose revoked_at is older than the retention window.

CREATE TABLE IF NOT EXISTS tokens (
    token_id      TEXT    PRIMARY KEY,                   -- ULID, lex-sortable
    hash          BLOB    NOT NULL UNIQUE,               -- sha256(raw token)
    name          TEXT    NOT NULL,                      -- operator-supplied label
    scope         TEXT    NOT NULL CHECK (scope IN ('read','send','admin')),
    created_at    INTEGER NOT NULL,                      -- unix seconds
    expires_at    INTEGER NOT NULL,                      -- unix seconds, 0 = never
    last_used_at  INTEGER,                               -- unix seconds, NULL = never used
    revoked_at    INTEGER                                -- unix seconds, NULL = active
);

-- Sweep helper.
CREATE INDEX IF NOT EXISTS idx_tokens_revoked ON tokens (revoked_at);
-- Lookup helper for `wad token list` (newest-first by issuance).
CREATE INDEX IF NOT EXISTS idx_tokens_created ON tokens (created_at DESC);
