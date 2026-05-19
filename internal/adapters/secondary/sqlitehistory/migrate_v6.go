package sqlitehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/006.up.sql
var schemaV6UpSQL string

//go:embed migrations/006.down.sql
var schemaV6DownSQL string

// migrateV6 applies spec-110h v5→v6 migration. Creates the
// media_transcripts table keyed on sha256 so a voice note forwarded
// across N chats stores one transcript row. CREATE TABLE IF NOT EXISTS
// keeps the migration safely re-runnable; PRAGMA user_version is the
// authoritative versioning gate.
func migrateV6(ctx context.Context, db *sql.DB) error {
	const ddl = `CREATE TABLE IF NOT EXISTS media_transcripts (
	    sha256      BLOB    NOT NULL PRIMARY KEY,
	    transcript  TEXT    NOT NULL DEFAULT '',
	    lang        TEXT    NOT NULL DEFAULT '',
	    adapter     TEXT    NOT NULL DEFAULT '',
	    created_at  INTEGER NOT NULL DEFAULT 0
	)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("sqlitehistory: v6 media_transcripts: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		return fmt.Errorf("sqlitehistory: v6 user_version: %w", err)
	}
	return nil
}

// DownV6 reverses migrateV6 by dropping the media_transcripts table.
// Audio payloads remain on disk; a follow-up `wa media download
// --transcribe` regenerates the rows.
func DownV6(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS media_transcripts"); err != nil {
		return fmt.Errorf("sqlitehistory: v6 down drop: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		return fmt.Errorf("sqlitehistory: v6 down user_version: %w", err)
	}
	return nil
}

// silence unused-warnings; the embedded SQL is retained so a future
// doctor command can emit the raw bytes (matches v4/v5 pattern).
var _, _ = schemaV6UpSQL, schemaV6DownSQL
