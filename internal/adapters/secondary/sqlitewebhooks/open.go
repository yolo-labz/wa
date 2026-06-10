// Package sqlitewebhooks owns webhooks.db — the durable endpoint
// registry + delivery queue backing feature 112 (standard-webhooks
// outbound delivery). Mirrors the sqliteschedule layout: WAL mode,
// single-writer daemon, lockedfile mutex, embedded schema.
package sqlitewebhooks

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

// Store owns the webhooks.db SQLite handle and its lockedfile mutex.
type Store struct {
	db     *sql.DB
	lock   *lockedfile.File
	dbPath string
}

// Open returns a Store rooted at dbPath, applying schema + migrations.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("sqlitewebhooks: dbPath must not be empty")
	}
	parent := filepath.Dir(dbPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("sqlitewebhooks: mkdir %s: %w", parent, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil { //nolint:gosec // 0700 is the intended dir mode (CLAUDE.md §FS layout)
		return nil, fmt.Errorf("sqlitewebhooks: chmod %s: %w", parent, err)
	}

	lock, err := lockedfile.Edit(dbPath + ".lock")
	if err != nil {
		return nil, fmt.Errorf("sqlitewebhooks: lock: %w", err)
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
		return nil, fmt.Errorf("sqlitewebhooks: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitewebhooks: ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitewebhooks: schema: %w", err)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			_ = lock.Close()
			return nil, fmt.Errorf("sqlitewebhooks: chmod db: %w", err)
		}
	}

	return &Store{db: db, lock: lock, dbPath: dbPath}, nil
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
