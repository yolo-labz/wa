package sqlitehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/005.up.sql
var schemaV5UpSQL string

//go:embed migrations/005.down.sql
var schemaV5DownSQL string

// migrateV5 applies spec-107 v4→v5 migration. Adds two nullable
// columns on messages so the daemon preserves the PN / LID duality
// whatsmeow surfaces via MessageInfo.AddressingMode + SenderAlt:
//
//	sender_alt_jid TEXT — alternative-namespace JID (PN if Sender is
//	                       LID, LID if Sender is PN), NULL when whatsmeow
//	                       did not yet know the mapping.
//	addressing_mode TEXT — "pn" or "lid", NULL on legacy rows.
//
// Each ALTER is guarded with hasColumn so the migration is safely
// re-runnable on a partially-applied schema (matches the v4 pattern).
func migrateV5(ctx context.Context, db *sql.DB) error {
	addCols := []struct{ name, ddl string }{
		{"sender_alt_jid", "ALTER TABLE messages ADD COLUMN sender_alt_jid TEXT"},
		{"addressing_mode", "ALTER TABLE messages ADD COLUMN addressing_mode TEXT"},
	}
	for _, col := range addCols {
		if hasColumn(ctx, db, "messages", col.name) {
			continue
		}
		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("sqlitehistory: v5 %s: %w", col.name, err)
		}
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		return fmt.Errorf("sqlitehistory: v5 user_version: %w", err)
	}
	return nil
}

// DownV5 reverses migrateV5 by rebuilding messages without the v5
// columns. Preserves every row's pre-v5 columns. Exposed for migration
// rehearsal + `wad migrate --down`.
func DownV5(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitehistory: v5 down begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const rebuild = `
CREATE TABLE messages_v4 (
    chat_jid       TEXT    NOT NULL,
    sender_jid     TEXT    NOT NULL,
    message_id     TEXT    NOT NULL,
    ts             INTEGER NOT NULL,
    body           TEXT    NOT NULL,
    media_type     TEXT    NOT NULL DEFAULT '',
    caption        TEXT    NOT NULL DEFAULT '',
    is_from_me     INTEGER NOT NULL DEFAULT 0,
    push_name      TEXT    NOT NULL DEFAULT '',
    raw_proto      BLOB,
    revoked_at     INTEGER,
    edited_at      INTEGER,
    previous_body  TEXT,
    edit_of        TEXT,
    interactive_json BLOB,
    PRIMARY KEY (message_id)
);
INSERT INTO messages_v4 (
    chat_jid, sender_jid, message_id, ts, body, media_type, caption,
    is_from_me, push_name, raw_proto, revoked_at, edited_at,
    previous_body, edit_of, interactive_json
)
SELECT
    chat_jid, sender_jid, message_id, ts, body, media_type, caption,
    is_from_me, push_name, raw_proto, revoked_at, edited_at,
    previous_body, edit_of, interactive_json
FROM messages;
DROP TABLE messages;
ALTER TABLE messages_v4 RENAME TO messages;
`
	for _, stmt := range splitSQL(rebuild) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlitehistory: v5 down rebuild: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		return fmt.Errorf("sqlitehistory: v5 down user_version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, schemaV5DownSQL); err != nil {
		return fmt.Errorf("sqlitehistory: v5 down sql: %w", err)
	}
	return tx.Commit()
}

// splitSQL splits a multi-statement string on semicolons. Whitespace-
// trimmed empty fragments are dropped. The migration SQL is hand-written
// and contains no embedded semicolons in string literals.
func splitSQL(s string) []string {
	out := make([]string, 0)
	cur := ""
	for _, r := range s {
		if r == ';' {
			trimmed := trimSpace(cur)
			if trimmed != "" {
				out = append(out, trimmed)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	trimmed := trimSpace(cur)
	if trimmed != "" {
		out = append(out, trimmed)
	}
	return out
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// silence unused-warning; schemaV5UpSQL is referenced via go:embed +
// reserved for any future migration that needs the raw SQL bytes
// (e.g., emitting them in a doctor command).
var _ = schemaV5UpSQL
