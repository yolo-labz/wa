package sqlitehistory

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupRetention is the hard cap on per-profile backup snapshots. The
// rotator keeps at most this many `messages.db.<ts>.bak` files under the
// profile's state/backups directory and unlinks the rest oldest-first.
const BackupRetention = 10

// BackupBeforeMigrate writes a timestamped copy of dbPath into
// backupsDir, creating the directory with mode 0700 if needed. The
// filename is `messages.db.<RFC3339>.bak` with colons replaced by
// hyphens so the path is portable across filesystems. The backup is
// produced via `VACUUM INTO` when the DB is a live SQLite file so the
// copy is WAL-consistent and transactionally clean; for non-SQLite or
// zero-byte paths it falls back to a plain byte copy.
//
// Returns the absolute path of the written backup. Callers record
// this in the migration_history.backup_path column.
func BackupBeforeMigrate(ctx context.Context, dbPath, backupsDir string, now time.Time) (string, error) {
	if dbPath == "" {
		return "", fmt.Errorf("sqlitehistory: backup: empty dbPath")
	}
	if backupsDir == "" {
		return "", fmt.Errorf("sqlitehistory: backup: empty backupsDir")
	}
	// Skip the backup when the source is missing (fresh install) — there
	// is nothing to preserve and the caller should still record the
	// migration row with an empty backup_path.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("sqlitehistory: backup: stat source: %w", err)
	}

	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return "", fmt.Errorf("sqlitehistory: backup: mkdir %s: %w", backupsDir, err)
	}
	if err := os.Chmod(backupsDir, 0o700); err != nil { //nolint:gosec // 0700 is the intended dir mode
		return "", fmt.Errorf("sqlitehistory: backup: chmod %s: %w", backupsDir, err)
	}

	stamp := strings.ReplaceAll(now.UTC().Format(time.RFC3339Nano), ":", "-")
	dst := filepath.Join(backupsDir, "messages.db."+stamp+".bak")

	if isSQLiteFile(dbPath) {
		if err := vacuumInto(ctx, dbPath, dst); err != nil {
			return "", fmt.Errorf("sqlitehistory: backup: VACUUM INTO: %w", err)
		}
	} else {
		if err := copyFile(dbPath, dst); err != nil {
			return "", fmt.Errorf("sqlitehistory: backup: copy: %w", err)
		}
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return "", fmt.Errorf("sqlitehistory: backup: chmod %s: %w", dst, err)
	}
	return dst, nil
}

// RotateBackups keeps the newest `keep` backups under backupsDir and
// unlinks the rest oldest-first (by mtime, ties broken by name). Files
// not matching the `messages.db.*.bak` pattern are ignored. Safe to
// call when the directory does not exist yet — returns nil in that case.
func RotateBackups(backupsDir string, keep int) error {
	if keep <= 0 {
		keep = BackupRetention
	}
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sqlitehistory: rotate: readdir: %w", err)
	}
	type entry struct {
		name  string
		mtime time.Time
	}
	var backups []entry
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "messages.db.") || !strings.HasSuffix(name, ".bak") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, entry{name: name, mtime: fi.ModTime()})
	}
	if len(backups) <= keep {
		return nil
	}
	// Newest first so the first `keep` entries survive.
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].mtime.Equal(backups[j].mtime) {
			return backups[i].name > backups[j].name
		}
		return backups[i].mtime.After(backups[j].mtime)
	})
	for _, b := range backups[keep:] {
		if err := os.Remove(filepath.Join(backupsDir, b.name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("sqlitehistory: rotate: remove %s: %w", b.name, err)
		}
	}
	return nil
}

// vacuumInto produces a WAL-consistent snapshot via `VACUUM INTO`. A
// fresh read-only handle on the source is used so in-flight writers on
// the primary connection keep operating. Opens with a short busy
// timeout and releases the handle before returning.
func vacuumInto(ctx context.Context, src, dst string) error {
	dsn := "file:" + src + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer db.Close() //nolint:errcheck // read-only handle
	// Remove any stale destination file — VACUUM INTO requires the
	// target path to be absent.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove dst: %w", err)
	}
	// Quote the dst path for the VACUUM INTO literal. SQLite's VACUUM
	// INTO does not accept bind parameters for the destination — the path
	// MUST be a SQL string literal, so we hand-escape single quotes per
	// the sqlite3 spec (sqlite.org/lang_expr.html §string literals).
	// dst is daemon-owned (always a sibling of session.db under
	// $XDG_DATA_HOME/wa/<profile>/) and never user-supplied on the wire,
	// but the escape is defence-in-depth.
	quoted := strings.ReplaceAll(dst, "'", "''")
	stmt := "VACUUM INTO '" + quoted + "'" //nolint:gosec // G202: VACUUM INTO requires literal path; dst hand-escaped above
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// copyFile is the non-SQLite fallback used when the source is missing
// the SQLite magic header (e.g. test fixtures, corrupted databases).
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // path validated by caller
	if err != nil {
		return err
	}
	defer in.Close()                                                       //nolint:errcheck // read-only close
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path validated by caller
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// isSQLiteFile reports whether path starts with the SQLite magic
// header (16 bytes, "SQLite format 3\000").
func isSQLiteFile(path string) bool {
	f, err := os.Open(path) //nolint:gosec // path validated by caller
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only close
	var header [16]byte
	n, _ := f.Read(header[:])
	if n != 16 {
		return false
	}
	return string(header[:]) == "SQLite format 3\x00"
}
