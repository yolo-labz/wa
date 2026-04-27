package sqlitehistory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestAutoMigrateBackupRotation confirms every up-migration through
// OpenWithBackups writes a new snapshot and rotates older snapshots
// past BackupRetention oldest-first.
func TestAutoMigrateBackupRotation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	backupsDir := filepath.Join(dir, "backups")

	// Seed a v3 database so OpenWithBackups has a real source to back up
	// before applying v4. We reach v3 via the migrateIfNeeded path with
	// no backups (opts=nil), then close the handle so the main Open can
	// take the lockedfile mutex.
	if err := seedV3DB(t, dbPath); err != nil {
		t.Fatalf("seedV3DB: %v", err)
	}

	// Pre-populate BackupRetention+5 stale snapshots with distinct
	// mtimes so rotation has something to prune.
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	base := time.Now().Add(-24 * time.Hour)
	for i := range BackupRetention + 5 {
		name := filepath.Join(backupsDir, fmt.Sprintf("messages.db.seed-%03d.bak", i))
		if err := os.WriteFile(name, []byte("seed"), 0o600); err != nil {
			t.Fatalf("seed backup: %v", err)
		}
		stamp := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	ctx := context.Background()
	store, err := OpenWithBackups(ctx, dbPath, backupsDir)
	if err != nil {
		t.Fatalf("OpenWithBackups: %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup

	// After OpenWithBackups: one fresh snapshot written + rotation to
	// BackupRetention total. The fresh snapshot must survive.
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var bakCount int
	sawFresh := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "messages.db.") || !strings.HasSuffix(name, ".bak") {
			continue
		}
		bakCount++
		if !strings.HasPrefix(name, "messages.db.seed-") {
			sawFresh = true
		}
	}
	if bakCount != BackupRetention {
		t.Fatalf("rotation kept %d files, want %d", bakCount, BackupRetention)
	}
	if !sawFresh {
		t.Fatal("fresh pre-v4 backup was rotated away — rotator must preserve newest")
	}
}

// TestMigrationHistoryPopulated asserts the v4 `up` path records an
// entry in migration_history with the real backup_path, not the empty
// string the SQL file self-inserts during earlier attempts.
func TestMigrationHistoryPopulated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	backupsDir := filepath.Join(dir, "backups")
	if err := seedV3DB(t, dbPath); err != nil {
		t.Fatalf("seedV3DB: %v", err)
	}

	ctx := context.Background()
	store, err := OpenWithBackups(ctx, dbPath, backupsDir)
	if err != nil {
		t.Fatalf("OpenWithBackups: %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup

	rows, err := store.db.QueryContext(ctx,
		`SELECT version, direction, backup_path FROM migration_history WHERE version = 4 AND direction = 'up' ORDER BY applied_at DESC`)
	if err != nil {
		t.Fatalf("select migration_history: %v", err)
	}
	defer rows.Close() //nolint:errcheck // read-only close

	var sawReal bool
	for rows.Next() {
		var version int
		var direction, backupPath string
		if err := rows.Scan(&version, &direction, &backupPath); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if backupPath == "" {
			continue
		}
		sawReal = true
		if !strings.Contains(backupPath, "messages.db.") || !strings.HasSuffix(backupPath, ".bak") {
			t.Fatalf("backup_path = %q; want messages.db.<ts>.bak shape", backupPath)
		}
		if _, err := os.Stat(backupPath); err != nil {
			t.Fatalf("backup file %q must exist on disk: %v", backupPath, err)
		}
	}
	if !sawReal {
		t.Fatal("migration_history has no v4/up row with a non-empty backup_path")
	}
}

// seedV3DB creates a fresh SQLite file, applies the embedded schema,
// and drives migrateIfNeeded to PRAGMA user_version = 3. Closes the
// handle before returning so the subsequent OpenWithBackups call can
// take the lockedfile mutex.
func seedV3DB(t *testing.T, dbPath string) error {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	// Run v2 + v3 migrations only. Then stamp user_version back to 3 so
	// OpenWithBackups sees pending v4.
	if err := migrateV2(ctx, db); err != nil {
		return fmt.Errorf("v2: %w", err)
	}
	if err := migrateV3(ctx, db); err != nil {
		return fmt.Errorf("v3: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("stamp v3: %w", err)
	}
	return nil
}
