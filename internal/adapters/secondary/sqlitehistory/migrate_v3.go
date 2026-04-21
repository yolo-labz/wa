package sqlitehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed schema_v3.sql
var schemaV3SQL string

// migrateV3 is additive: it adds message_receipts, message_idempotency, and
// an interactive_json column on messages. ADD COLUMN is O(1) in SQLite — no
// data rewrite. Idempotent on re-run.
func migrateV3(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaV3SQL); err != nil {
		return fmt.Errorf("sqlitehistory: v3 schema: %w", err)
	}
	if !hasColumn(ctx, db, "messages", "interactive_json") {
		const q = `ALTER TABLE messages ADD COLUMN interactive_json BLOB`
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("sqlitehistory: v3 add interactive_json: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("sqlitehistory: v3 user_version: %w", err)
	}
	return nil
}
