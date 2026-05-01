-- 005.down.sql — spec 107 schema v5 → v4
-- SQLite does not support DROP COLUMN before 3.35; even on newer SQLite
-- the table-rebuild pattern is the safe option for foreign-key-bearing
-- schemas. The Go-side DownV5 in migrate_v5.go performs the rebuild.
-- This file is reserved for any pre-rebuild work the SQL must do; it
-- is intentionally a no-op so RecordMigration still has a row to insert.
INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (5, 'down', CAST(strftime('%s','now') AS INTEGER), '');
