package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// sessionMetaSchema is the single-row table that persists
// device-identity facts the whatsmeow sqlstore does not expose directly.
// Feature 017 T3-21 uses the is_business column to gate labels.* RPCs
// without re-issuing a GetBusinessProfile on every daemon boot.
//
// Single-row pattern: rowid is pinned to 1 via the CHECK constraint so
// concurrent UPSERTs cannot split the table.
const sessionMetaSchema = `
CREATE TABLE IF NOT EXISTS session_meta (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    is_business INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL,
    paired_at   INTEGER NOT NULL DEFAULT 0
);
`

// EnsureSessionMetaSchema creates the session_meta table if it does not
// already exist, then adds any column a database created by an older
// build is missing. Safe to call multiple times.
func EnsureSessionMetaSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, sessionMetaSchema); err != nil {
		return fmt.Errorf("sqlitestore: session_meta schema: %w", err)
	}
	// paired_at (issue #311) postdates the original two-column table.
	// SQLite has no ADD COLUMN IF NOT EXISTS, and swallowing the
	// "duplicate column name" error would also swallow real ones, so ask
	// the schema instead.
	has, err := hasColumn(ctx, db, "session_meta", "paired_at")
	if err != nil {
		return err
	}
	if !has {
		const alter = `ALTER TABLE session_meta ADD COLUMN paired_at INTEGER NOT NULL DEFAULT 0`
		if _, err := db.ExecContext(ctx, alter); err != nil {
			return fmt.Errorf("sqlitestore: session_meta add paired_at: %w", err)
		}
	}
	return nil
}

// hasColumn reports whether table already carries column.
func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, fmt.Errorf("sqlitestore: table_info %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("sqlitestore: table_info %s: %w", table, err)
	}
	return found, nil
}

// SetIsBusiness upserts the is_business flag. updatedAt is a unix epoch
// so the call site controls clock injection in tests.
func SetIsBusiness(ctx context.Context, db *sql.DB, isBusiness bool, updatedAt int64) error {
	if err := EnsureSessionMetaSchema(ctx, db); err != nil {
		return err
	}
	const q = `
		INSERT INTO session_meta (id, is_business, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET is_business = excluded.is_business,
		                              updated_at   = excluded.updated_at
	`
	flag := 0
	if isBusiness {
		flag = 1
	}
	if _, err := db.ExecContext(ctx, q, flag, updatedAt); err != nil {
		return fmt.Errorf("sqlitestore: set is_business: %w", err)
	}
	return nil
}

// IsBusiness reads the persisted flag. Returns (false, nil) if the row
// has not been written yet — the daemon treats unknown as personal
// until GetBusinessProfile resolves.
func IsBusiness(ctx context.Context, db *sql.DB) (bool, error) {
	if err := EnsureSessionMetaSchema(ctx, db); err != nil {
		return false, err
	}
	var flag int
	err := db.QueryRowContext(ctx, `SELECT is_business FROM session_meta WHERE id = 1`).Scan(&flag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlitestore: read is_business: %w", err)
	}
	return flag == 1, nil
}
