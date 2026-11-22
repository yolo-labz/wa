package sqlitetokens

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestLookup_ReportsCurrentValidityByID: the admin session re-reads a token
// by id on every request: Lookup tells live, revoked, expired and unknown apart.
func TestLookup_ReportsCurrentValidityByID(t *testing.T) {
	store := openFreshStore(t)
	ctx := t.Context()
	live, _ := store.Issue(ctx, "live", ScopeSend, time.Hour)
	got, err := store.Lookup(ctx, live.ID)
	if err != nil || got.Name != "live" || got.Scope != ScopeSend || got.Raw != "" || got.ExpiresAt.IsZero() {
		t.Fatalf("live lookup = %+v, %v", got, err)
	}

	if _, err := store.Lookup(ctx, "01H0000000000000000000000Z"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("unknown id = %v, want ErrTokenNotFound", err)
	}
	_ = store.Revoke(ctx, live.ID)
	if got, err := store.Lookup(ctx, live.ID); !errors.Is(err, ErrTokenRevoked) || got.RevokedAt.IsZero() {
		t.Errorf("revoked lookup = %+v, %v; want ErrTokenRevoked with RevokedAt", got, err)
	}
	short, _ := store.Issue(ctx, "short", ScopeRead, time.Nanosecond)
	<-time.After(1100 * time.Millisecond) // expires_at has 1 s resolution
	if got, err := store.Lookup(ctx, short.ID); !errors.Is(err, ErrTokenExpired) || got.ID != short.ID {
		t.Errorf("expired lookup = %+v, %v; want ErrTokenExpired with the row", got, err)
	}
}

// TestCheck_AdminScopeAndDenials: Check answers "is this an admin token that
// is still valid", reading SQLite directly.
func TestCheck_AdminScopeAndDenials(t *testing.T) {
	store := openFreshStore(t)
	ctx := t.Context()
	adm, _ := store.Issue(ctx, "adm", ScopeAdmin, 0)
	snd, _ := store.Issue(ctx, "snd", ScopeSend, 0)
	if ok, err := store.Check(ctx, adm.ID); !ok || err != nil {
		t.Errorf("admin = %v, %v; want true, nil", ok, err)
	}
	if ok, err := store.Check(ctx, snd.ID); ok || err != nil {
		t.Errorf("send = %v, %v; want false, nil (valid, not admin)", ok, err)
	}
	_ = store.Revoke(ctx, adm.ID)
	if ok, err := store.Check(ctx, adm.ID); ok || !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("revoked admin = %v, %v; want false, ErrTokenRevoked", ok, err)
	}
	short, _ := store.Issue(ctx, "short", ScopeAdmin, time.Nanosecond)
	<-time.After(1100 * time.Millisecond)
	if ok, err := store.Check(ctx, short.ID); ok || !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expired admin = %v, %v; want false, ErrTokenExpired", ok, err)
	}
}

// TestSweep_KeepsRecentlyRevoked: Sweep(olderThan>0) removes only rows whose
// revocation is older than the window; Sweep(0) removes every inactive row.
func TestSweep_KeepsRecentlyRevoked(t *testing.T) {
	store := openFreshStore(t)
	ctx := t.Context()
	live, _ := store.Issue(ctx, "live", ScopeRead, 0)
	dead, _ := store.Issue(ctx, "dead", ScopeRead, 0)
	_ = store.Revoke(ctx, dead.ID)
	if n, err := store.Sweep(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("sweep(1h) = %d, %v; a just-revoked row must stay", n, err)
	}
	if n, err := store.Sweep(ctx, 0); err != nil || n != 1 {
		t.Fatalf("sweep(0) = %d, %v; want exactly the revoked row", n, err)
	}
	if _, err := store.Lookup(ctx, live.ID); err != nil {
		t.Errorf("live token lost by sweep: %v", err)
	}
	if _, err := store.Lookup(ctx, dead.ID); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("swept token = %v, want ErrTokenNotFound", err)
	}
}

// TestFlushPending_SkipsCorruptKeysAndWritesTheRest: a pending key that is
// not hex is dropped, and the valid one still reaches the row.
func TestFlushPending_SkipsCorruptKeysAndWritesTheRest(t *testing.T) {
	store := openFreshStore(t)
	ctx := t.Context()
	tok, _ := store.Issue(ctx, "u", ScopeRead, 0)
	if _, err := store.Verify(ctx, tok.Raw); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.pending["not-hex!"] = 1
	store.mu.Unlock()
	if err := store.FlushPending(ctx); err != nil {
		t.Fatalf("flush with a corrupt key = %v, want nil", err)
	}
	rows, _ := store.List(ctx)
	if len(rows) != 1 || rows[0].LastUsedAt.IsZero() {
		t.Fatalf("valid pending write lost: %+v", rows)
	}
	if err := store.FlushPending(ctx); err != nil {
		t.Errorf("empty flush = %v, want nil", err)
	}
}

// TestClosedStore_FailsLoudAndNeverAsTokenInvalid: every operation on a
// closed store errors with its own prefix and is never a definitive
// domain.ErrTokenInvalid, so callers keep the session instead of denying.
func TestClosedStore_FailsLoudAndNeverAsTokenInvalid(t *testing.T) {
	store := openFreshStore(t)
	ctx := t.Context()
	tok, _ := store.Issue(ctx, "x", ScopeAdmin, 0)
	if _, err := store.Verify(ctx, tok.Raw); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	_, e1 := store.Issue(ctx, "y", ScopeRead, 0)
	_, e2 := store.Verify(ctx, tok.Raw)
	_, e3 := store.Lookup(ctx, tok.ID)
	_, e4 := store.List(ctx)
	_, e5 := store.Sweep(ctx, 0)
	_, e6 := store.Check(ctx, tok.ID)
	for i, err := range []error{e1, e2, e3, store.Revoke(ctx, tok.ID), e4, e5, e6} {
		if err == nil || errors.Is(err, domain.ErrTokenInvalid) || !strings.HasPrefix(err.Error(), "sqlitetokens: ") {
			t.Errorf("op %d on a closed store = %v, want a sqlitetokens error that is not ErrTokenInvalid", i, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

// TestClose_FlushFailureIsBestEffort: a flush that cannot write errors on
// its own, and Close still returns nil after it.
func TestClose_FlushFailureIsBestEffort(t *testing.T) {
	store := openFreshStore(t)
	store.touch([]byte("h"), time.Now())
	_ = store.db.Close()
	if err := store.FlushPending(t.Context()); err == nil || !strings.Contains(err.Error(), "flush") {
		t.Fatalf("flush on a dead handle = %v, want a flush error", err)
	}
	store.touch([]byte("h"), time.Now())
	if err := store.Close(); err != nil {
		t.Errorf("Close = %v, want nil: the flush failure is best effort", err)
	}
}

// TestOpen_RefusesBadPathsAndTightensModes covers Open's failure modes and
// its file-mode contract (dir 0700, file 0600).
func TestOpen_RefusesBadPathsAndTightensModes(t *testing.T) {
	ctx := t.Context()
	if _, err := Open(ctx, ""); err == nil || !strings.Contains(err.Error(), "dbPath") {
		t.Errorf("empty path = %v", err)
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, filepath.Join(blocker, "tokens.db")); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Errorf("parent is a file = %v, want a mkdir error", err)
	}
	if _, err := Open(ctx, dir); err == nil {
		t.Error("a directory as the database file opened")
	}
	// A wrong-shaped tokens table fails the schema step.
	bad := filepath.Join(dir, "bad.db")
	db, err := sql.Open("sqlite", bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE tokens (token_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(ctx, bad); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Errorf("wrong-shaped tokens table = %v, want a schema error", err)
	}

	nested := filepath.Join(dir, "a", "b", "tokens.db")
	s, err := Open(ctx, nested)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if st, err := os.Stat(nested); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("db file mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(filepath.Dir(nested)); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("parent mode = %v, %v; want 0700", st.Mode().Perm(), err)
	}
}
