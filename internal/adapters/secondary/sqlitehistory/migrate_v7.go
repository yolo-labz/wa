package sqlitehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/007.up.sql
var schemaV7UpSQL string

//go:embed migrations/007.down.sql
var schemaV7DownSQL string

// correctMessagesAUTrigger is the fixed AFTER UPDATE trigger: the FTS
// re-index insert lists two columns (rowid, body) matching its two VALUES.
// The 'delete' insert keeps the special messages_fts command column.
const correctMessagesAUTrigger = `CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END`

// brokenMessagesAUTrigger is the pre-v7 definition kept verbatim so DownV7
// is a faithful inverse. It is intentionally malformed (three columns, two
// VALUES) and reintroduces #171 — see 007.down.sql.
const brokenMessagesAUTrigger = `CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES (new.rowid, new.body);
END`

// migrateV7 fixes #171: the v1 base schema created messages_au with an FTS
// re-index insert binding two VALUES to three columns, so every UPDATE on
// messages (e.g. StampEdit during `wa msg edit`) fired the trigger and
// failed with "2 values for 3 columns" / -32603. No prior migration touched
// the trigger, so every database carries the broken definition regardless of
// its user_version; this step drops and recreates it. DROP + CREATE is
// re-runnable; PRAGMA user_version is the authoritative gate.
func migrateV7(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS messages_au"); err != nil {
		return fmt.Errorf("sqlitehistory: v7 drop trigger: %w", err)
	}
	if _, err := db.ExecContext(ctx, correctMessagesAUTrigger); err != nil {
		return fmt.Errorf("sqlitehistory: v7 create trigger: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		return fmt.Errorf("sqlitehistory: v7 user_version: %w", err)
	}
	return nil
}

// DownV7 reverses migrateV7 by restoring the pre-v7 (broken) trigger so the
// rollback returns to an exact v6 schema. This reintroduces #171 by design;
// see 007.down.sql for the operator warning.
func DownV7(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS messages_au"); err != nil {
		return fmt.Errorf("sqlitehistory: v7 down drop trigger: %w", err)
	}
	if _, err := db.ExecContext(ctx, brokenMessagesAUTrigger); err != nil {
		return fmt.Errorf("sqlitehistory: v7 down create trigger: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		return fmt.Errorf("sqlitehistory: v7 down user_version: %w", err)
	}
	return nil
}

// silence unused-warnings; the embedded SQL is retained so a future
// doctor command can emit the raw bytes (matches v4/v5/v6 pattern).
var _, _ = schemaV7UpSQL, schemaV7DownSQL
