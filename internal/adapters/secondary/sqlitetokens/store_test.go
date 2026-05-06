package sqlitetokens

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openFreshStore returns a Store backed by a fresh tokens.db under
// t.TempDir(). The store is closed via t.Cleanup.
func openFreshStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestIssue_HappyPath pins the on-issue contract: returns a
// hex-encoded 32-byte raw token, populates id/scope/createdAt, and
// honours the TTL by setting a non-zero ExpiresAt.
func TestIssue_HappyPath(t *testing.T) {
	store := openFreshStore(t)
	tok, err := store.Issue(t.Context(), "operator-laptop", ScopeAdmin, 24*time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok.ID == "" {
		t.Error("token id empty")
	}
	if got := len(tok.Raw); got != 64 {
		t.Errorf("raw token len = %d, want 64 hex chars", got)
	}
	if tok.Scope != ScopeAdmin {
		t.Errorf("scope = %q, want admin", tok.Scope)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt zero with non-zero ttl")
	}
}

// TestIssue_InvalidScope pins fail-closed on bad scope.
func TestIssue_InvalidScope(t *testing.T) {
	store := openFreshStore(t)
	if _, err := store.Issue(t.Context(), "x", Scope("godmode"), 0); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("Issue invalid scope = %v, want ErrInvalidScope", err)
	}
}

// TestVerify_RoundTrip pins issue → verify by raw token.
func TestVerify_RoundTrip(t *testing.T) {
	store := openFreshStore(t)
	issued, err := store.Issue(t.Context(), "label", ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Verify(t.Context(), issued.Raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.ID != issued.ID {
		t.Errorf("verify id = %q, want %q", got.ID, issued.ID)
	}
	if got.Scope != ScopeRead {
		t.Errorf("verify scope = %q, want read", got.Scope)
	}
}

// TestVerify_UnknownToken pins ErrTokenNotFound on garbage input.
func TestVerify_UnknownToken(t *testing.T) {
	store := openFreshStore(t)
	if _, err := store.Verify(t.Context(), "deadbeef"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("Verify unknown = %v, want ErrTokenNotFound", err)
	}
}

// TestRevoke_Idempotent pins the revoke contract: first call sets
// revoked_at, subsequent calls succeed without changing state.
func TestRevoke_Idempotent(t *testing.T) {
	store := openFreshStore(t)
	issued, _ := store.Issue(t.Context(), "x", ScopeAdmin, 0)

	if err := store.Revoke(t.Context(), issued.ID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	// Second revoke is a no-op (still nil).
	if err := store.Revoke(t.Context(), issued.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}

	// Verify after revoke returns ErrTokenRevoked.
	if _, err := store.Verify(t.Context(), issued.Raw); !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("verify after revoke = %v, want ErrTokenRevoked", err)
	}
}

// TestRevoke_Unknown pins ErrTokenNotFound on unknown id.
func TestRevoke_Unknown(t *testing.T) {
	store := openFreshStore(t)
	if err := store.Revoke(t.Context(), "01H0000000000000000000000Z"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("revoke unknown = %v, want ErrTokenNotFound", err)
	}
}

// TestVerify_Expired pins ErrTokenExpired when expires_at < now.
func TestVerify_Expired(t *testing.T) {
	store := openFreshStore(t)
	// Issue with a 1ns TTL so the verify call sees expired.
	issued, err := store.Issue(t.Context(), "expiring", ScopeRead, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	// Wait a moment to ensure expires_at < now.
	<-time.After(10 * time.Millisecond)
	if _, err := store.Verify(t.Context(), issued.Raw); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("verify expired = %v, want ErrTokenExpired", err)
	}
}

// TestList_NewestFirst pins the listing contract: every issued
// token appears in the result set, and revoked tokens still appear
// (with RevokedAt populated). Strict newest-first ordering is best-
// effort within a second — created_at has 1s resolution, so two
// tokens issued in the same second can come back in either order.
func TestList_NewestFirst(t *testing.T) {
	store := openFreshStore(t)
	first, _ := store.Issue(t.Context(), "first", ScopeRead, 0)
	// Sleep > 1s so the two tokens land in different created_at
	// buckets and the DESC sort is deterministic.
	<-time.After(1100 * time.Millisecond)
	second, _ := store.Issue(t.Context(), "second", ScopeSend, 0)
	_ = store.Revoke(t.Context(), first.ID)

	rows, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID != second.ID {
		t.Errorf("rows[0] = %s, want %s (newest-first)", rows[0].ID, second.ID)
	}
	// Find the revoked row regardless of position.
	var revokedRow *Token
	for i := range rows {
		if rows[i].ID == first.ID {
			revokedRow = &rows[i]
			break
		}
	}
	if revokedRow == nil {
		t.Fatal("revoked first token not in list")
	}
	if revokedRow.RevokedAt.IsZero() {
		t.Error("revoked token RevokedAt zero")
	}
}

// TestSweep pins that revoked or expired rows older than the cutoff
// are deleted; active rows survive.
func TestSweep(t *testing.T) {
	store := openFreshStore(t)
	active, _ := store.Issue(t.Context(), "active", ScopeRead, 0)
	revoked, _ := store.Issue(t.Context(), "revoked", ScopeRead, 0)
	_ = store.Revoke(t.Context(), revoked.ID)

	// Aggressive sweep — drop everything inactive immediately.
	n, err := store.Sweep(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("sweep deleted = %d, want 1", n)
	}

	rows, _ := store.List(t.Context())
	if len(rows) != 1 || rows[0].ID != active.ID {
		t.Errorf("after sweep rows = %+v, want only active token", rows)
	}
}

// TestVerify_LastUsedAtBuffered pins that touch + flush updates the
// last_used_at column.
func TestVerify_LastUsedAtBuffered(t *testing.T) {
	store := openFreshStore(t)
	issued, _ := store.Issue(t.Context(), "x", ScopeAdmin, 0)

	if _, err := store.Verify(t.Context(), issued.Raw); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	// Buffered — not yet on disk.
	rows, _ := store.List(t.Context())
	if !rows[0].LastUsedAt.IsZero() {
		t.Logf("last_used_at flushed eagerly (acceptable, but unexpected)")
	}

	if err := store.FlushPending(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	rows, _ = store.List(t.Context())
	if rows[0].LastUsedAt.IsZero() {
		t.Error("last_used_at zero after flush")
	}
}

// TestIssue_BlankNameRejected pins the empty-name guard (operator
// can't issue an unlabeled token).
func TestIssue_BlankNameRejected(t *testing.T) {
	store := openFreshStore(t)
	if _, err := store.Issue(t.Context(), "  ", ScopeAdmin, 0); err == nil ||
		!strings.Contains(err.Error(), "name") {
		t.Errorf("blank name = %v, want name-required error", err)
	}
}

// TestStore_PeriodicFlusher pins the Codex-fix contract for
// last_used_at: a Store with a short flushTTL writes pending updates
// to disk on its own cadence, not just at Close(). Without this
// invariant a crash loses every last_used_at since process start,
// which destroys the forensic value of the column.
func TestStore_PeriodicFlusher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	store, err := Open(t.Context(), path, WithFlushInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issued, _ := store.Issue(t.Context(), "x", ScopeAdmin, 0)
	if _, err := store.Verify(t.Context(), issued.Raw); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Wait up to 5s for the flusher to drain pending writes; the
	// next List MUST show a non-zero last_used_at WITHOUT us calling
	// FlushPending() explicitly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := store.List(t.Context())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) == 1 && !rows[0].LastUsedAt.IsZero() {
			return // pass
		}
		<-time.After(100 * time.Millisecond)
	}
	t.Fatal("background flusher did not write last_used_at within 5s")
}

// silence unused-context ref
var _ = context.Background
