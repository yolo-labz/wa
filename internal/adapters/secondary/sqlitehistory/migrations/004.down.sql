-- 004.down.sql — feature 018 schema v4 → v3
-- Preserves every non-revoked original row. Edit rows (edit_of NOT NULL)
-- are dropped; the original messages they referenced remain, losing
-- their edited_at metadata. Revoked rows are dropped.
-- Column set below mirrors the actual v3 shape (v1 + v2 additive +
-- v3 additive: includes raw_proto, media_type, caption, is_from_me,
-- push_name, interactive_json).

CREATE TABLE messages_new (
    rowid            INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_jid         TEXT    NOT NULL,
    sender_jid       TEXT    NOT NULL,
    message_id       TEXT    NOT NULL,
    ts               INTEGER NOT NULL,
    body             TEXT    NOT NULL DEFAULT '',
    raw_proto        BLOB,
    media_type       TEXT    NOT NULL DEFAULT '',
    caption          TEXT    NOT NULL DEFAULT '',
    is_from_me       INTEGER NOT NULL DEFAULT 0,
    push_name        TEXT    NOT NULL DEFAULT '',
    interactive_json BLOB,
    UNIQUE (chat_jid, message_id)
);

INSERT INTO messages_new (
    rowid, chat_jid, sender_jid, message_id, ts, body, raw_proto,
    media_type, caption, is_from_me, push_name, interactive_json
)
SELECT rowid, chat_jid, sender_jid, message_id, ts, body, raw_proto,
       media_type, caption, is_from_me, push_name, interactive_json
FROM messages
WHERE revoked_at IS NULL
  AND edit_of IS NULL;

-- FTS5 triggers/table are rebuilt by the application layer after
-- rename to avoid replicating trigger bodies here.
DROP TRIGGER IF EXISTS messages_ai;
DROP TRIGGER IF EXISTS messages_ad;
DROP TRIGGER IF EXISTS messages_au;
DROP TABLE IF EXISTS messages_fts;

DROP INDEX IF EXISTS idx_messages_chat_revoked;
DROP INDEX IF EXISTS idx_messages_chat_ts;

DROP TABLE messages;
ALTER TABLE messages_new RENAME TO messages;

CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages (chat_jid, ts DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    body,
    content='messages',
    content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);

-- Rebuild FTS content from messages.
INSERT INTO messages_fts(messages_fts) VALUES('rebuild');

CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES (new.rowid, new.body);
END;

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS migration_history;
