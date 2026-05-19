-- 006.up.sql — spec 110h schema v5 → v6
-- Adds media_transcripts table for voice-note transcripts. Keyed on
-- sha256 (content-addressed) so a voice note forwarded across N chats
-- stores exactly one transcript row. FR-054/FR-055 + FR-004.
--
-- adapter is the lowercase selector name ("whispercpp", "hear", "groq")
-- — informational only, NOT a foreign key. Distinct adapters can
-- overwrite each other's row via Upsert; the daemon trusts whoever
-- wrote last.
--
-- created_at is unix seconds. No updated_at column — Upsert overwrites
-- in place, the operator can join against migration_history if forensic
-- ordering matters.

CREATE TABLE IF NOT EXISTS media_transcripts (
    sha256      BLOB    NOT NULL PRIMARY KEY,
    transcript  TEXT    NOT NULL DEFAULT '',
    lang        TEXT    NOT NULL DEFAULT '',
    adapter     TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT 0
);

INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (6, 'up', CAST(strftime('%s','now') AS INTEGER), '');
