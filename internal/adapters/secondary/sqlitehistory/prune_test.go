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

// writeStaleBackups drops n `messages.db.<i>.bak` files into dir with
// strictly increasing mtimes (one minute apart) so prune ordering is
// deterministic even when the test runs faster than filesystem mtime
// granularity. It returns the file names ordered oldest→newest.
func writeStaleBackups(t *testing.T, dir string, n int) []string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	base := time.Now().Add(-24 * time.Hour)
	names := make([]string, 0, n)
	for i := range n {
		name := fmt.Sprintf("messages.db.snap-%03d.bak", i)
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("snap"), 0o600); err != nil {
			t.Fatalf("write backup: %v", err)
		}
		stamp := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(full, stamp, stamp); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		names = append(names, name)
	}
	return names
}

// listBackups returns the surviving `messages.db.*.bak` names in dir.
func listBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "messages.db.") && strings.HasSuffix(name, ".bak") {
			out = append(out, name)
		}
	}
	return out
}

// TestPruneBackupsKeepsNewestN seeds maxBackups+6 snapshots with distinct
// mtimes and asserts pruneBackups keeps exactly the newest maxBackups and
// removes the rest oldest-first (issue #202).
func TestPruneBackupsKeepsNewestN(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	total := maxBackups + 6
	names := writeStaleBackups(t, dir, total) // oldest→newest

	if err := pruneBackups(dir, maxBackups); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}

	survivors := listBackups(t, dir)
	if len(survivors) != maxBackups {
		t.Fatalf("pruneBackups kept %d files, want %d", len(survivors), maxBackups)
	}

	// The survivors must be the newest maxBackups names (the tail of the
	// oldest→newest slice). Every older name must be gone.
	keep := make(map[string]bool, maxBackups)
	for _, n := range names[total-maxBackups:] {
		keep[n] = true
	}
	for _, s := range survivors {
		if !keep[s] {
			t.Fatalf("survivor %q is not among the newest %d backups", s, maxBackups)
		}
	}
	for _, removed := range names[:total-maxBackups] {
		if _, err := os.Stat(filepath.Join(dir, removed)); !os.IsNotExist(err) {
			t.Fatalf("expected oldest backup %q to be pruned, stat err=%v", removed, err)
		}
	}
}

// TestPruneBackupsSameSecondMtime exercises the tie-break: when all
// snapshots share one mtime, pruneBackups falls back to name ordering so
// it still keeps exactly maxBackups (lexicographically-largest names).
func TestPruneBackupsSameSecondMtime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stamp := time.Now().Add(-time.Hour)
	total := maxBackups + 4
	for i := range total {
		full := filepath.Join(dir, fmt.Sprintf("messages.db.tie-%03d.bak", i))
		if err := os.WriteFile(full, []byte("tie"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chtimes(full, stamp, stamp); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	if err := pruneBackups(dir, maxBackups); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}

	survivors := listBackups(t, dir)
	if len(survivors) != maxBackups {
		t.Fatalf("same-mtime prune kept %d, want %d", len(survivors), maxBackups)
	}
	// Highest-numbered (lexicographically-largest) names win the tie-break.
	for _, s := range survivors {
		if s < fmt.Sprintf("messages.db.tie-%03d.bak", total-maxBackups) {
			t.Fatalf("tie-break kept %q but should keep only the highest-named %d", s, maxBackups)
		}
	}
}

// TestPruneBackupsBelowCapNoop confirms a directory at or under the cap
// is left untouched, and a missing directory is a silent no-op.
func TestPruneBackupsBelowCapNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStaleBackups(t, dir, maxBackups)
	if err := pruneBackups(dir, maxBackups); err != nil {
		t.Fatalf("pruneBackups at cap: %v", err)
	}
	if got := len(listBackups(t, dir)); got != maxBackups {
		t.Fatalf("prune at cap removed files: have %d, want %d", got, maxBackups)
	}

	missing := filepath.Join(dir, "does-not-exist")
	if err := pruneBackups(missing, maxBackups); err != nil {
		t.Fatalf("pruneBackups on missing dir must be nil, got %v", err)
	}
}

// TestPruneBackupsIgnoresForeignFiles confirms non-`messages.db.*.bak`
// files are never counted toward the cap nor deleted.
func TestPruneBackupsIgnoresForeignFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeStaleBackups(t, dir, maxBackups+3)
	foreign := []string{"README.txt", "messages.db", "session.db.bak", "notes.bak"}
	for _, f := range foreign {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatalf("write foreign %q: %v", f, err)
		}
	}

	if err := pruneBackups(dir, maxBackups); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}

	if got := len(listBackups(t, dir)); got != maxBackups {
		t.Fatalf("prune kept %d matching backups, want %d", got, maxBackups)
	}
	for _, f := range foreign {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("foreign file %q must survive prune: %v", f, err)
		}
	}
}

// TestOpenWithBackupsPrunesOnStartup is the regression test for the prod
// bug (#202): a fully-migrated DB writes NO new migration backup, yet a
// pre-existing pile of >maxBackups snapshots must still be swept back to
// the cap on every OpenWithBackups call.
func TestOpenWithBackupsPrunesOnStartup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "messages.db")
	backupsDir := filepath.Join(dir, "backups")

	// Seed a DB already at the latest schema version so migrateIfNeeded is
	// a no-op and the only thing that can shrink the pile is the new
	// prune-on-open path.
	seedLatestDB(t, dbPath)

	pile := maxBackups + 6
	writeStaleBackups(t, backupsDir, pile)

	ctx := context.Background()
	store, err := OpenWithBackups(ctx, dbPath, backupsDir)
	if err != nil {
		t.Fatalf("OpenWithBackups: %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup

	survivors := listBackups(t, backupsDir)
	if len(survivors) != maxBackups {
		t.Fatalf("startup prune kept %d backups, want %d (no migration fired)", len(survivors), maxBackups)
	}
}

// seedLatestDB creates a fresh SQLite file, applies the embedded schema,
// and drives every migration so PRAGMA user_version reaches the head.
// The subsequent OpenWithBackups therefore runs no migration step.
func seedLatestDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	// opts=nil disables backup-before-migrate; this just advances
	// user_version to the head so the real Open sees nothing pending.
	if err := migrateIfNeeded(ctx, db, nil); err != nil {
		t.Fatalf("seed migrate to head: %v", err)
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read seeded user_version: %v", err)
	}
	if version < 7 {
		t.Fatalf("seedLatestDB reached v%d, expected >= 7 (head)", version)
	}
}
