-- 007.up.sql — fix #171 schema v6 → v7
-- Repairs the messages_au AFTER UPDATE trigger. The v1 base schema (and
-- the 004.down rollback) created the FTS re-index insert with a column
-- list of three (messages_fts, rowid, body) but only two VALUES, so any
-- UPDATE on messages — e.g. sqlitehistory.StampEdit during `wa msg edit`
-- — fired the trigger and failed with SQLite "2 values for 3 columns",
-- surfacing as JSON-RPC -32603. The wire edit still left the box, so the
-- recipient saw the new text while the local row kept the stale body
-- (daemon/remote divergence).
--
-- Every database created from v1 carries the broken trigger regardless of
-- its current user_version (no prior migration touched messages_au), so
-- this step drops and recreates it for all existing installs. New
-- databases get the corrected trigger straight from schema.sql.
--
-- DROP + CREATE is idempotent: re-running replaces the trigger with an
-- identical definition. PRAGMA user_version is the authoritative gate.

DROP TRIGGER IF EXISTS messages_au;

CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;

INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (7, 'up', CAST(strftime('%s','now') AS INTEGER), '');
