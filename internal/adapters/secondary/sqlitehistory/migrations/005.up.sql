-- 005.up.sql — spec 107 schema v4 → v5
-- Adds sender_alt_jid + addressing_mode columns on messages so the
-- daemon preserves both PN and LID forms for every inbound message.
-- WhatsApp surfaces this duality in MessageInfo.SenderAlt /
-- MessageInfo.AddressingMode (whatsmeow types/message.go); pre-spec-105
-- the adapter discarded both. Spec 106 added the resolver port; spec
-- 107 persists the mapping so callers (the AI agent) can render either
-- form without a round-trip.
--
-- Both columns are nullable. NULL means "whatsmeow did not surface this
-- form yet" — most legacy rows (pre-v5 schema) and the history-sync
-- path land here. Callers MUST treat NULL as "unknown" without
-- erroring (matches spec 106 FR-002 zero-JID semantics).
--
-- ALTER TABLE ADD COLUMN is O(1) in SQLite — no data rewrite.
ALTER TABLE messages ADD COLUMN sender_alt_jid TEXT;
ALTER TABLE messages ADD COLUMN addressing_mode TEXT;

-- Record this migration's own run. backup_path left empty here; the
-- application layer writes its own row with the real backup path
-- immediately after this SQL succeeds (matches the v4 pattern).
INSERT INTO migration_history (version, direction, applied_at, backup_path)
VALUES (5, 'up', CAST(strftime('%s','now') AS INTEGER), '');
