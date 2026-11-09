package admin

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
)

// WA-ADMIN-SEC F8: a captured pre-login cookie and form token must not be
// replayable.
func TestAdmin_PreLoginTokenIsSingleUse(t *testing.T) {
	f := newFixture(t)
	good := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	pre, csrf := f.loginPage(t, "")
	post := func(raw string) *http.Response {
		return f.do(req{method: http.MethodPost, path: "/admin/login", form: url.Values{"csrf": {csrf}, "token": {raw}}, cookies: []*http.Cookie{pre}}).Result()
	}
	if got := post("wrong").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("first attempt = %d, want 401", got)
	}
	if got := post(good.Raw).StatusCode; got != http.StatusForbidden {
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
