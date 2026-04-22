package sqlitehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/004.up.sql
var schemaV4UpSQL string

//go:embed migrations/004.down.sql
var schemaV4DownSQL string

// migrateV4 applies feature-018 v3→v4 migration. Adds revoke/edit
// metadata columns, idempotency_keys sidecar, migration_history. The
// ALTER TABLE steps are not idempotent under sqlite so we guard each
// ADD COLUMN with hasColumn. Feature-018 FR-013 + FR-015a.
func migrateV4(ctx context.Context, db *sql.DB) error {
	addCols := []struct{ name, ddl string }{
		{"revoked_at", "ALTER TABLE messages ADD COLUMN revoked_at INTEGER"},
		{"edited_at", "ALTER TABLE messages ADD COLUMN edited_at INTEGER"},
		{"previous_body", "ALTER TABLE messages ADD COLUMN previous_body TEXT"},
		{"edit_of", "ALTER TABLE messages ADD COLUMN edit_of TEXT"},
	}
	for _, col := range addCols {
		if hasColumn(ctx, db, "messages", col.name) {
			continue
		}
		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("sqlitehistory: v4 %s: %w", col.name, err)
		}
	}
	const idx = `CREATE INDEX IF NOT EXISTS idx_messages_chat_revoked
	    ON messages (chat_jid, revoked_at)`
	if _, err := db.ExecContext(ctx, idx); err != nil {
		return fmt.Errorf("sqlitehistory: v4 idx: %w", err)
	}
	const idem = `CREATE TABLE IF NOT EXISTS idempotency_keys (
	    method        TEXT    NOT NULL,
	    profile       TEXT    NOT NULL,
	    key           TEXT    NOT NULL,
	    params_hash   BLOB    NOT NULL,
	    response_json BLOB    NOT NULL,
	    created_at    INTEGER NOT NULL,
	    expires_at    INTEGER NOT NULL,
	    PRIMARY KEY (method, profile, key)
	)`
	if _, err := db.ExecContext(ctx, idem); err != nil {
		return fmt.Errorf("sqlitehistory: v4 idempotency_keys: %w", err)
	}
	const idemIdx = `CREATE INDEX IF NOT EXISTS idx_idempotency_expires
	    ON idempotency_keys (expires_at)`
	if _, err := db.ExecContext(ctx, idemIdx); err != nil {
		return fmt.Errorf("sqlitehistory: v4 idempotency expiry idx: %w", err)
	}
	const hist = `CREATE TABLE IF NOT EXISTS migration_history (
	    version     INTEGER NOT NULL,
	    direction   TEXT    NOT NULL CHECK (direction IN ('up','down')),
	    applied_at  INTEGER NOT NULL,
	    backup_path TEXT    NOT NULL
	)`
	if _, err := db.ExecContext(ctx, hist); err != nil {
		return fmt.Errorf("sqlitehistory: v4 migration_history: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		return fmt.Errorf("sqlitehistory: v4 user_version: %w", err)
	}
	return nil
}

// RecordMigration inserts a migration_history row. Called by the
// composition root after migration so it can pass in the real backup
// path (sidecar SQL inserts the empty string because it does not know
// the path). Direction must be "up" or "down".
func RecordMigration(ctx context.Context, db *sql.DB, version int, direction, backupPath string, appliedAt int64) error {
	if direction != "up" && direction != "down" {
		return fmt.Errorf("sqlitehistory: bad direction %q", direction)
	}
	const q = `INSERT INTO migration_history (version, direction, applied_at, backup_path) VALUES (?, ?, ?, ?)`
	if _, err := db.ExecContext(ctx, q, version, direction, appliedAt, backupPath); err != nil {
		return fmt.Errorf("sqlitehistory: record migration: %w", err)
	}
	return nil
}

// DownV4 reverses migrateV4. Preserves non-revoked non-edit messages
// via table-rebuild. Exposed for migration rehearsal + `wad migrate
// --down`. Drops revoke/edit metadata columns.
func DownV4(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaV4DownSQL); err != nil {
		return fmt.Errorf("sqlitehistory: v4 down: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("sqlitehistory: v4 down user_version: %w", err)
	}
	return nil
}
