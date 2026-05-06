package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rogpeppe/go-internal/lockedfile"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	// Register the modernc.org/sqlite driver under the name "sqlite" so
	// sqlstore.New(ctx, "sqlite", ...) resolves without CGO.
	_ "modernc.org/sqlite"
)

// Store wraps a whatsmeow *sqlstore.Container with a process-wide
// lockedfile mutex on the database path and tightened file permissions.
// Per CLAUDE.md §"Daemon, IPC, single-instance" the lock guarantees that
// no two daemons ever write the ratchet store concurrently, since
// whatsmeow's sqlstore does not lock on its own.
type Store struct {
	container *sqlstore.Container
	lock      *lockedfile.File
	dbPath    string
}

// Open ensures the parent directory exists with mode 0700, acquires an
// exclusive lockedfile lock on dbPath+".lock", opens the whatsmeow
// sqlstore Container against a foreign-keys-enabled SQLite URL, and
// chmods the database file to 0600. The returned *Store satisfies the
// sessionContainer interface in internal/adapters/secondary/whatsmeow.
//
// log may be nil; whatsmeow defaults to a no-op logger in that case.
func Open(ctx context.Context, dbPath string, log waLog.Logger) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("sqlitestore: dbPath must not be empty")
	}

	parent := filepath.Dir(dbPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("sqlitestore: mkdir %s: %w", parent, err)
	}
	// Tighten in case the directory already existed with looser perms.
	if err := os.Chmod(parent, 0o700); err != nil { //nolint:gosec // 0700 is the intended dir mode (CLAUDE.md §FS layout)
		return nil, fmt.Errorf("sqlitestore: chmod %s: %w", parent, err)
	}

	lock, err := lockedfile.Edit(dbPath + ".lock")
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: acquire lock %s: %w", dbPath+".lock", err)
	}

	// Spec 016 FR-014 / T049 + T050: open the *sql.DB ourselves (rather
	// than via sqlstore.New) so we can (a) apply trusted_schema=OFF and
	// cell_size_check=ON to the same security baseline as messages.db,
	// and (b) run PRAGMA quick_check at startup against the ratchet
	// store. quick_check catches cosmetic corruption (out-of-order pages,
	// orphaned freelist entries) before whatsmeow tries to read it —
	// failure here is far cheaper to recover from than mid-session
	// SIGNAL_NO_SESSION fallout.
	dsn := "file:" + dbPath +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=trusted_schema(OFF)" +
		"&_pragma=cell_size_check(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitestore: open db: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitestore: ping: %w", err)
	}
	// quick_check returns a single row "ok" on a healthy database; any
	// other row is an error description to surface verbatim.
	var quickCheck string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quickCheck); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitestore: quick_check: %w", err)
	}
	if quickCheck != "ok" {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitestore: quick_check failed: %s", quickCheck)
	}
	container := sqlstore.NewWithDB(db, "sqlite", log)
	if err := container.Upgrade(ctx); err != nil {
		_ = container.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitestore: upgrade container: %w", err)
	}

	// Chmod the database file once SQLite has created it. Best-effort:
	// if the file does not yet exist (sqlstore.New is lazy on some
	// drivers) we silently skip — the daemon's umask plus the 0700
	// parent directory still bound the blast radius.
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = container.Close()
			_ = lock.Close()
			return nil, fmt.Errorf("sqlitestore: chmod %s: %w", dbPath, err)
		}
	}

	return &Store{container: container, lock: lock, dbPath: dbPath}, nil
}

// Container returns the wrapped whatsmeow sqlstore container. The
// daemon passes this to whatsmeow.NewClient via *store.Device.
func (s *Store) Container() *sqlstore.Container { return s.container }

// Close closes the underlying sqlstore container and releases the
// lockedfile, joining any errors via errors.Join so a failure releasing
// the lock does not hide a failure closing the container.
func (s *Store) Close() error {
	var containerErr, lockErr error
	if s.container != nil {
		containerErr = s.container.Close()
	}
	if s.lock != nil {
		lockErr = s.lock.Close()
	}
	return errors.Join(containerErr, lockErr)
}
