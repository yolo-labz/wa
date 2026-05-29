-- 007.down.sql — reverse #171 fix, schema v7 → v6
-- Restores the messages_au trigger to its exact pre-v7 definition so the
-- rollback is a faithful inverse and migration_history stays truthful.
--
-- WARNING: the pre-v7 trigger is the BROKEN one (three columns, two
-- VALUES). Rolling back to v6 reintroduces #171 — `wa msg edit` will
-- again fail with "2 values for 3 columns" / -32603 while still emitting
-- the wire edit. Only run this to return to an exact v6 schema for
-- forensic/bisect purposes; do not run it on a daemon expected to serve
-- edits.

DROP TRIGGER IF EXISTS messages_au;

CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES (new.rowid, new.body);
END;

INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (7, 'down', CAST(strftime('%s','now') AS INTEGER), '');
