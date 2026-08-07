-- 008.up.sql — fix #315 schema v7 → v8
-- Adds messages.caption_folded, the accent- and case-folded search key that
-- --caption is matched against. SQLite's LIKE folds ASCII case only, so
-- against a caption reading "Segue nosso catálogo" both the shouted
-- "CATÁLOGO" and the unaccented "catalogo" a phone keyboard produces matched
-- NOTHING — an empty result that reads as "no such media" rather than "you
-- typed it without the accent". --caption is what the CLI offers instead of
-- dumping every caption in a group, so a filter that silently under-matches
-- pushes the caller back to picking rows by timestamp proximity.
--
-- ALTER TABLE ADD COLUMN is O(1) in SQLite; the backfill that follows is the
-- only row-touching step, and it is scoped to rows that actually carry a
-- caption (a small minority — text messages store '').
--
-- The backfill itself CANNOT be expressed here: folding is NFD-decompose,
-- drop combining marks, lowercase, and SQLite ships no such function. It runs
-- in Go, one transaction, in migrateV8. This file is the operator-readable
-- record of the DDL half, matching the v4-v7 pattern; the authoritative
-- implementation is migrate_v8.go.
--
-- No index: every --caption query is a leading-wildcard LIKE ('%needle%'),
-- which no B-tree can serve. Adding one would cost writes and buy nothing —
-- and DROP COLUMN in 008.down.sql refuses to run on an indexed column.

ALTER TABLE messages ADD COLUMN caption_folded TEXT NOT NULL DEFAULT '';

-- Backfill (Go-side, shown for reference — see migrateV8):
--   SELECT rowid, caption FROM messages WHERE caption != '';
--   UPDATE messages SET caption_folded = ? WHERE rowid = ?;

INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (8, 'up', CAST(strftime('%s','now') AS INTEGER), '');
