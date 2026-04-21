-- sqlitehistory v3 additive migration. FR-012/013 (receipts + interactive),
-- FR-031 (message-scoped idempotency). All statements are idempotent.

CREATE TABLE IF NOT EXISTS message_receipts (
    chat_jid     TEXT NOT NULL,
    message_id   TEXT NOT NULL,
    kind         INTEGER NOT NULL,
    jid          TEXT NOT NULL,
    ts           INTEGER NOT NULL,
    PRIMARY KEY (chat_jid, message_id, jid, kind)
);

CREATE INDEX IF NOT EXISTS idx_message_receipts_chat_msg
    ON message_receipts (chat_jid, message_id);

CREATE TABLE IF NOT EXISTS message_idempotency (
    profile          TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    fingerprint      TEXT NOT NULL,
    result_json      BLOB NOT NULL,
    created_at       INTEGER NOT NULL,
    expires_at       INTEGER NOT NULL,
    PRIMARY KEY (profile, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_message_idempotency_expiry
    ON message_idempotency (expires_at);
