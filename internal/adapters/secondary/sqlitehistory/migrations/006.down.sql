-- 006.down.sql — reverse spec 110h schema v6 → v5
-- Drops the media_transcripts table. Operator-facing flag — running
-- this discards every cached transcript. The audio payload itself
-- remains on disk under $XDG_CACHE_HOME/wa/media/ (content-addressed),
-- so a subsequent `wa media download --transcribe` regenerates the row.

DROP TABLE IF EXISTS media_transcripts;

INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (6, 'down', CAST(strftime('%s','now') AS INTEGER), '');
