-- 008.down.sql — reverse #315, schema v8 → v7
-- Drops messages.caption_folded so the rollback returns to an exact v7
-- schema and migration_history stays truthful.
--
-- No data loss: caption_folded is a derived search key, never a source of
-- truth. Every folded value is recomputable from `caption`, which this step
-- does not touch, so a later re-migration to v8 rebuilds it exactly.
--
-- WARNING: rolling back to v7 reintroduces #315 — --caption goes back to
-- ASCII-only case folding, where "catalogo" and "CATÁLOGO" both miss a
-- caption reading "catálogo" and the caller gets an empty result that reads
-- as "no such media".
--
-- DROP COLUMN requires SQLite 3.35+ (modernc.org/sqlite is well past it) and
-- refuses to run on an indexed or generated column. caption_folded is
-- deliberately neither.

ALTER TABLE messages DROP COLUMN caption_folded;

INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (8, 'down', CAST(strftime('%s','now') AS INTEGER), '');
