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
    updated_at  INTEGER NOT NULL
);
`

// EnsureSessionMetaSchema creates the session_meta table if it does not
// already exist. Safe to call multiple times.
func EnsureSessionMetaSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, sessionMetaSchema); err != nil {
		return fmt.Errorf("sqlitestore: session_meta schema: %w", err)
	}
	return nil
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
