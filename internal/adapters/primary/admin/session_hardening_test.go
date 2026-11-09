package admin

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
)

// WA-ADMIN-SEC F8: a captured pre-login cookie and form token must not be
// replayable.
func TestAdmin_PreLoginTokenIsSingleUse(t *testing.T) {
	f := newFixture(t)
	good := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	pre, csrf := f.loginPage(t, "")
	post := func(raw string) int {
		return f.do(req{method: http.MethodPost, path: "/admin/login", form: url.Values{"csrf": {csrf}, "token": {raw}}, cookies: []*http.Cookie{pre}}).Code
	}
	if got := post("wrong"); got != http.StatusUnauthorized {
		t.Fatalf("first attempt = %d, want 401", got)
	}
	if got := post(good.Raw); got != http.StatusForbidden {
		t.Fatalf("replayed pair with a GOOD token = %d, want 403", got)
	}
	if !f.signedIn(mustSession(t, f, good.Raw)) {
		t.Fatal("a fresh form must still log in")
	}
}

func mustSession(t *testing.T, f *fixture, raw string) *http.Cookie {
	t.Helper()
	sess, _ := f.login(t, raw)
	return sess
}

// WA-ADMIN-SEC F9: a transient store error refuses the request but must not
// end the session; a revoked token still does.
func TestAdmin_TransientStoreErrorDeniesRequestKeepsSession(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	sess, _ := f.login(t, tok.Raw)
	f.shim.lookupFail.Store(true)
	if f.signedIn(sess) {
		t.Fatal("request served while the store errored: must fail closed")
	}
	f.shim.lookupFail.Store(false)
	if !f.signedIn(sess) {
		t.Fatal("session lost to a transient store error")
	}
	if err := f.other.Revoke(t.Context(), tok.ID); err != nil {
		t.Fatal(err)
	}
	if f.signedIn(sess) {
		t.Fatal("revoked token still backs a session")
	}
	f.shim.lookupFail.Store(false)
	if f.signedIn(sess) {
		t.Fatal("a revoked session must stay dropped")
	}
}

// WA-ADMIN-SEC F9 (sweeper half): a sweep that hits a transient store error
// must keep every session; only a gone token ends one.
func TestAdmin_SweepKeepsSessionsOnTransientStoreError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); f.s.runSweeper(ctx) }()
		count := func() int { f.s.mu.Lock(); defer f.s.mu.Unlock(); return len(f.s.sessions) }

		tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
		sess, _ := f.login(t, tok.Raw)
		f.shim.lookupFail.Store(true)
		<-time.After(SweepInterval)
		synctest.Wait()
		if n := count(); n != 1 {
			t.Fatalf("a sweep during a transient store error dropped sessions: %d left, want 1", n)
		}
		f.shim.lookupFail.Store(false)
		if !f.signedIn(sess) {
			t.Fatal("session unusable after the store recovered")
		}
		if err := f.other.Revoke(t.Context(), tok.ID); err != nil {
			t.Fatal(err)
		}
		<-time.After(SweepInterval)
		synctest.Wait()
		if n := count(); n != 0 {
			t.Fatalf("a revoked token still backs %d sessions after a healthy sweep", n)
		}
		cancel()
		<-done
	})
}
