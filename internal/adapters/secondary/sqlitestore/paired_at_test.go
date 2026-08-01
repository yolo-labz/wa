package sqlitestore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
)

// TestPairedAtRoundTrip covers the three states the pairing timestamp can
// be in: never recorded (the honest answer for every session paired before
// issue #311), recorded and surviving a store reopen, and cleared back to
// unknown when the device is unlinked.
func TestPairedAtRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "session.db")

	store, err := sqlitestore.Open(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, known, err := store.PairedAt(ctx); err != nil || known {
		t.Fatalf("initial PairedAt: got (known=%v, err=%v), want (false, nil)", known, err)
	}

	// Second-granularity: the column is a unix epoch, so a sub-second
	// component cannot survive the round trip and the test must not
	// pretend otherwise.
	want := time.Date(2026, 7, 14, 9, 30, 0, 0, time.UTC)
	if err := store.SetPairedAt(ctx, want); err != nil {
		t.Fatalf("SetPairedAt: %v", err)
	}
	got, known, err := store.PairedAt(ctx)
	if err != nil || !known {
		t.Fatalf("PairedAt after set: got (known=%v, err=%v), want (true, nil)", known, err)
	}
	if !got.Equal(want) {
		t.Fatalf("PairedAt = %s, want %s", got, want)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Survives the process restart the daemon performs on every deploy —
	// the whole reason the timestamp is persisted instead of cached.
	reopened, err := sqlitestore.Open(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, known, err = reopened.PairedAt(ctx)
	if err != nil || !known {
		t.Fatalf("PairedAt after reopen: got (known=%v, err=%v), want (true, nil)", known, err)
	}
	if !got.Equal(want) {
		t.Fatalf("PairedAt after reopen = %s, want %s", got, want)
	}

	// Unlinking clears it: the timestamp described a device that is gone.
	if err := reopened.SetPairedAt(ctx, time.Time{}); err != nil {
		t.Fatalf("SetPairedAt zero: %v", err)
	}
	if _, known, err := reopened.PairedAt(ctx); err != nil || known {
		t.Fatalf("PairedAt after clear: got (known=%v, err=%v), want (false, nil)", known, err)
	}
}

// TestEnsureSessionMetaSchemaMigratesLegacyTable pins the ALTER TABLE
// branch: a session.db written by a build that predates issue #311 has a
// session_meta table without paired_at, and CREATE TABLE IF NOT EXISTS
// alone would leave it that way.
func TestEnsureSessionMetaSchemaMigratesLegacyTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const legacy = `
	CREATE TABLE session_meta (
	    id          INTEGER PRIMARY KEY CHECK (id = 1),
	    is_business INTEGER NOT NULL DEFAULT 0,
	    updated_at  INTEGER NOT NULL
	);
	INSERT INTO session_meta (id, is_business, updated_at) VALUES (1, 1, 1700000000);
	`
	if _, err := db.ExecContext(ctx, legacy); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}

	// Twice: the migration must be idempotent, since every PairedAt and
	// SetPairedAt call runs it.
	for i := range 2 {
		if err := sqlitestore.EnsureSessionMetaSchema(ctx, db); err != nil {
			t.Fatalf("EnsureSessionMetaSchema call %d: %v", i+1, err)
		}
	}

	var cols int
	err = db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('session_meta') WHERE name = 'paired_at'`).Scan(&cols)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	if cols != 1 {
		t.Fatalf("paired_at column count = %d, want 1", cols)
	}

	// The migration must not disturb the row it is adding a column to.
	isBusiness, err := sqlitestore.IsBusiness(ctx, db)
	if err != nil {
		t.Fatalf("IsBusiness: %v", err)
	}
	if !isBusiness {
		t.Fatal("is_business = false after migration, want the seeded true")
	}

	var epoch int64
	if err := db.QueryRowContext(ctx, `SELECT paired_at FROM session_meta WHERE id = 1`).Scan(&epoch); err != nil {
		t.Fatalf("read paired_at: %v", err)
	}
	if epoch != 0 {
		t.Fatalf("migrated paired_at = %d, want 0 (unknown — there is nothing to back-fill from)", epoch)
	}
}
