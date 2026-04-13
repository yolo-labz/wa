package sqlitehistory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// migrateIfNeeded checks PRAGMA user_version and applies pending
// migrations. Feature 009 — spec FR-020, FR-021.
func migrateIfNeeded(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("sqlitehistory: read user_version: %w", err)
	}

	if version < 2 {
		if err := migrateV2(ctx, db); err != nil {
			return fmt.Errorf("sqlitehistory: migrate v1→v2: %w", err)
		}
	}

	return nil
}

// migrateV2 adds media_type, caption, is_from_me, and push_name
// columns. ALTER TABLE ADD COLUMN is O(1) in SQLite — no data
// rewrite. Existing v1 rows get safe defaults (empty string, 0).
// Each column addition is idempotent (skips if column already exists).
func migrateV2(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"media_type", "ALTER TABLE messages ADD COLUMN media_type TEXT NOT NULL DEFAULT ''"},
		{"caption", "ALTER TABLE messages ADD COLUMN caption TEXT NOT NULL DEFAULT ''"},
		{"is_from_me", "ALTER TABLE messages ADD COLUMN is_from_me INTEGER NOT NULL DEFAULT 0"},
		{"push_name", "ALTER TABLE messages ADD COLUMN push_name TEXT NOT NULL DEFAULT ''"},
	}

	for _, col := range columns {
		if hasColumn(ctx, db, "messages", col.name) {
			continue
		}
		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("exec %q: %w", col.ddl, err)
		}
	}

	// PRAGMA user_version must run outside a transaction in SQLite.
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}

	return nil
}

// hasColumn reports whether the messages table has a column with the given name.
// The table name is hardcoded to "messages" to avoid SQL injection.
func hasColumn(ctx context.Context, db *sql.DB, _, column string) bool {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(messages)")
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if strings.EqualFold(name, column) {
			return true
		}
	}
	return false
}
