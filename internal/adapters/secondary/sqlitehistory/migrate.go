package sqlitehistory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MigrateOpts configures migrateIfNeeded. Zero value (passed as nil by
// legacy callers) disables backup-before-migrate. When BackupsDir is
// set, every `up` migration writes a `messages.db.<ts>.bak` snapshot
// into that directory, records the resulting path in migration_history,
// and keeps newest-first BackupRetention snapshots.
type MigrateOpts struct {
	DBPath     string
	BackupsDir string
	Now        func() time.Time
}

// migrateIfNeeded checks PRAGMA user_version and applies pending
// migrations. Feature 009 — spec FR-020, FR-021. Feature 018 FR-013
// adds the backup-before-migrate contract via MigrateOpts. Spec 107
// adds the v4→v5 step. Each migration step is a flat translation of
// "if version < N, run migrateVN, record migration"; extracting the
// pattern into a helper would scatter the migration audit trail.
//
//nolint:gocyclo // flat migration chain; each step is one branch
func migrateIfNeeded(ctx context.Context, db *sql.DB, opts *MigrateOpts) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("sqlitehistory: read user_version: %w", err)
	}

	if version < 2 {
		if err := migrateV2(ctx, db); err != nil {
			return fmt.Errorf("sqlitehistory: migrate v1→v2: %w", err)
		}
	}
	if version < 3 {
		if err := migrateV3(ctx, db); err != nil {
			return fmt.Errorf("sqlitehistory: migrate v2→v3: %w", err)
		}
	}
	if version < 4 {
		backupPath, err := maybeBackup(ctx, opts)
		if err != nil {
			return fmt.Errorf("sqlitehistory: backup before v4: %w", err)
		}
		if err := migrateV4(ctx, db); err != nil {
			return fmt.Errorf("sqlitehistory: migrate v3→v4: %w", err)
		}
		if err := RecordMigration(ctx, db, 4, "up", backupPath, migrateNow(opts).Unix()); err != nil {
			return fmt.Errorf("sqlitehistory: record migration v4: %w", err)
		}
		if opts != nil && opts.BackupsDir != "" {
			if err := RotateBackups(opts.BackupsDir, BackupRetention); err != nil {
				return fmt.Errorf("sqlitehistory: rotate backups: %w", err)
			}
		}
	}
	if version < 5 {
		backupPath, err := maybeBackup(ctx, opts)
		if err != nil {
			return fmt.Errorf("sqlitehistory: backup before v5: %w", err)
		}
		if err := migrateV5(ctx, db); err != nil {
			return fmt.Errorf("sqlitehistory: migrate v4→v5: %w", err)
		}
		if err := RecordMigration(ctx, db, 5, "up", backupPath, migrateNow(opts).Unix()); err != nil {
			return fmt.Errorf("sqlitehistory: record migration v5: %w", err)
		}
		if opts != nil && opts.BackupsDir != "" {
			if err := RotateBackups(opts.BackupsDir, BackupRetention); err != nil {
				return fmt.Errorf("sqlitehistory: rotate backups: %w", err)
			}
		}
	}

	return nil
}

// maybeBackup returns the backup path to record in migration_history.
// Empty string when backups are disabled or the source file does not
// exist (fresh install). Callers still record the migration row.
func maybeBackup(ctx context.Context, opts *MigrateOpts) (string, error) {
	if opts == nil || opts.BackupsDir == "" || opts.DBPath == "" {
		return "", nil
	}
	return BackupBeforeMigrate(ctx, opts.DBPath, opts.BackupsDir, migrateNow(opts))
}

func migrateNow(opts *MigrateOpts) time.Time {
	if opts != nil && opts.Now != nil {
		return opts.Now()
	}
	return time.Now().UTC()
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
	defer closeRows(rows, "hasColumn")

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
