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
	return PanicArtefacts{
		SessionDB: sess,
		HistoryDB: hist,
		AuditLog:  audit,
		Lockfile:  lock,
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
		art.AuditLog, art.Lockfile,
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
