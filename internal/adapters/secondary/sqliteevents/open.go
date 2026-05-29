// Package sqliteevents is the secondary adapter owning events.db — the
// per-profile durable ring-buffer backing the event subscribe stream.
// Implements app.EventBuffer per FR-060..FR-064.
//
// Capacity defaults to 10 000 rows, drop-oldest on append when full. The
// running total of drops is surfaced via Stats so the subscribe layer can
// synthesise `stream.drop` events when a resuming client falls behind.
package sqliteevents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rogpeppe/go-internal/lockedfile"

	_ "modernc.org/sqlite"
)

// DefaultCapacity is the FR-062 ring-buffer capacity.
const DefaultCapacity = 10_000

// Store owns the events.db handle and its lockedfile mutex.
type Store struct {
	db       *sql.DB
	lock     *lockedfile.File
	dbPath   string
	capacity int
}

// Open returns a Store rooted at dbPath with the given capacity. A capacity
// of 0 or below falls back to DefaultCapacity.
func Open(ctx context.Context, dbPath string, capacity int) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("sqliteevents: dbPath must not be empty")
	}
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	parent := filepath.Dir(dbPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("sqliteevents: mkdir %s: %w", parent, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil { //nolint:gosec // 0700 is the intended dir mode (CLAUDE.md §FS layout)
		return nil, fmt.Errorf("sqliteevents: chmod %s: %w", parent, err)
	}

	lock, err := lockedfile.Edit(dbPath + ".lock")
	if err != nil {
		return nil, fmt.Errorf("sqliteevents: lock: %w", err)
	}

	dsn := "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-32000)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("sqliteevents: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqliteevents: ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqliteevents: schema: %w", err)
	}
	if err := migrateIfNeeded(ctx, db); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqliteevents: migrate: %w", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE events_meta SET capacity = ? WHERE id = 1`, capacity); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqliteevents: set capacity: %w", err)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			_ = lock.Close()
			return nil, fmt.Errorf("sqliteevents: chmod db: %w", err)
		}
	}

	return &Store{db: db, lock: lock, dbPath: dbPath, capacity: capacity}, nil
}

// Close releases the SQLite handle and lockedfile mutex.
func (s *Store) Close() error {
	var dbErr, lockErr error
	if s.db != nil {
		dbErr = s.db.Close()
	}
	if s.lock != nil {
		lockErr = s.lock.Close()
	}
	return errors.Join(dbErr, lockErr)
}
