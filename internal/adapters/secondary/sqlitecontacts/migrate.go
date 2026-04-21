package sqlitecontacts

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateIfNeeded reads PRAGMA user_version and applies pending migrations.
// v1 is the initial schema; no future migrations have landed yet.
func migrateIfNeeded(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version < 1 {
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	}
	return nil
}
