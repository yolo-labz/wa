package whatsmeow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// artefactTempPaths seeds a temp directory with non-empty bytes at each
// artefact path the Panic method removes. Returns the populated struct
// so tests can assert on the exact fields.
func artefactTempPaths(t *testing.T) PanicArtefacts {
	t.Helper()
	dir := t.TempDir()
	sess := filepath.Join(dir, "session.db")
	hist := filepath.Join(dir, "messages.db")
	audit := filepath.Join(dir, "audit.log")
	lock := filepath.Join(dir, "wa.sock.lock")
	for _, p := range []string{
		sess, sess + "-wal", sess + "-shm",
		hist, hist + "-wal", hist + "-shm",
		audit, lock,
	} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	media := filepath.Join(dir, "media", "sha256")
	if err := os.MkdirAll(filepath.Join(media, "ab"), 0o700); err != nil {
		t.Fatalf("seed media tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(media, "ab", "blob"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed media blob: %v", err)
	}
	return PanicArtefacts{
		SessionDB:      sess,
		HistoryDB:      hist,
		AuditLog:       audit,
		Lockfile:       lock,
		MediaCacheRoot: media,
	}
}

// TestPanicWipesAllArtefacts: every configured path (including SQLite
// sidecars) is removed after Panic returns nil. Missing paths do not
// error — ENOENT is treated as success.
func TestPanicWipesAllArtefacts(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	art := artefactTempPaths(t)
	a.SetPanicArtefacts(art)

	if err := a.Panic(context.Background(), "test"); err != nil {
		t.Fatalf("Panic: %v", err)
	}

	for _, p := range []string{
		art.SessionDB, art.SessionDB + "-wal", art.SessionDB + "-shm",
		art.HistoryDB, art.HistoryDB + "-wal", art.HistoryDB + "-shm",
		art.AuditLog, art.Lockfile, art.MediaCacheRoot,
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("path %s still exists after Panic (stat err=%v)", p, err)
		}
	}

	// Second call must be a no-op.
	if err := a.Panic(context.Background(), "test-second"); err != nil {
		t.Fatalf("Panic second call returned error: %v", err)
	}
}

// TestPanicSymlinkArtefactNotFollowed (018 audit SEC-05): a symlink
// planted at an artefact path is unlinked — never followed — and the
// wipe reports an error because the pointed-at data survived.
func TestPanicSymlinkArtefactNotFollowed(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.db")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "audit.log")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}

	errs := removePanicArtefacts(PanicArtefacts{AuditLog: link})
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one symlink-anomaly error", errs)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("symlink target was destroyed: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink itself should be unlinked (lstat err=%v)", err)
	}
}

// TestPanicMediaCacheSymlinkRootNotFollowed (018 audit SEC-05): a
// symlinked media-cache root must never aim the recursive delete at a
// foreign directory.
func TestPanicMediaCacheSymlinkRootNotFollowed(t *testing.T) {
	dir := t.TempDir()
	victimDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(victimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	victimFile := filepath.Join(victimDir, "keep.txt")
	if err := os.WriteFile(victimFile, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "media")
	if err := os.Symlink(victimDir, link); err != nil {
		t.Fatal(err)
	}

	errs := removePanicArtefacts(PanicArtefacts{MediaCacheRoot: link})
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one symlink-anomaly error", errs)
	}
	if _, err := os.Stat(victimFile); err != nil {
		t.Errorf("foreign tree was destroyed through symlinked root: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink itself should be unlinked (lstat err=%v)", err)
	}
}

// TestPanicAuditRowOnSuccess: a successful Panic deposits an
// AuditPanic row carrying the reason supplied by the caller.
func TestPanicAuditRowOnSuccess(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	const reason = "rpc:test-case"
	if err := a.Panic(context.Background(), reason); err != nil {
		t.Fatalf("Panic: %v", err)
	}

	entries := a.auditBuf.Snapshot()
	var found bool
	for _, e := range entries {
		if e.Action == domain.AuditPanic && e.Decision == "wiped" && e.Detail == reason {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected AuditPanic[wiped] row with detail=%q; got %d entries", reason, len(entries))
	}
}

// TestPanicLockfileRemoved: the lockfile path specifically is unlinked
// so the next wad start can acquire a fresh flock.
func TestPanicLockfileRemoved(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	dir := t.TempDir()
	lock := filepath.Join(dir, "wa.sock.lock")
	if err := os.WriteFile(lock, []byte(""), 0o600); err != nil {
		t.Fatalf("seed lockfile: %v", err)
	}

	a.SetPanicArtefacts(PanicArtefacts{Lockfile: lock})
	if err := a.Panic(context.Background(), "rpc"); err != nil {
		t.Fatalf("Panic: %v", err)
	}

	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lockfile %s still exists after Panic (err=%v)", lock, err)
	}
}
