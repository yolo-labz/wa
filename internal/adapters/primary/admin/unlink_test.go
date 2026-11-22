package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func (f *fixture) unlinkPost(sess *http.Cookie, csrf, confirm string) (code int, body string) {
	rec := f.do(req{
		method: http.MethodPost, path: "/admin/session/logout", cookies: []*http.Cookie{sess},
		form: url.Values{"csrf": {csrf}, "confirm": {confirm}, "key": {"k1"}},
	})
	return rec.Code, rec.Body.String()
}

// TestAdminLogout_RequiresTypedConfirm (FR-116-03, T12): no GET, link or
// wrong name unlinks; only the typed profile name does.
func TestAdminLogout_RequiresTypedConfirm(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)

	page := f.do(req{path: "/admin/session/logout", cookies: []*http.Cookie{sess}})
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="confirm"`) {
		t.Fatalf("confirm page = %d %q", page.Code, page.Body.String())
	}
	for _, typed := range []string{"", "defaul", "DEFAULT", " default", "default "} {
		if code, body := f.unlinkPost(sess, csrf, typed); code != http.StatusBadRequest || strings.Contains(body, "Unlinked.") {
			t.Errorf("typed %q = %d, want 400 and no unlink", typed, code)
		}
	}
	if n := len(f.rpc.logouts); n != 0 {
		t.Fatalf("session.logout ran %d times without the typed name", n)
	}
	if got := f.audit.decisions(domain.AuditUnlink); len(got) != 0 {
		t.Fatalf("a refused confirmation left audit rows %v", got)
	}
	if code, body := f.unlinkPost(sess, csrf, "default"); code != http.StatusOK || !strings.Contains(body, "Unlinked.") {
		t.Fatalf("typed name = %d %q", code, body)
	}
	if n := len(f.rpc.logouts); n != 1 {
		t.Fatalf("session.logout calls = %v, want one", f.rpc.logouts)
	}
}

// TestAdminLogout_CSRFAndAudit (SR-116-06/08, R3): CSRF first; the intent row
// precedes the RPC and fails closed; a failed unlink is recorded failed and
// never reported as done; an outcome failure after the effect is 503.
func TestAdminLogout_CSRFAndAudit(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	rows := func() string { return strings.Join(f.audit.decisions(domain.AuditUnlink), ",") }

	if code, _ := f.unlinkPost(sess, "wrong", "default"); code != http.StatusForbidden || len(f.rpc.logouts) != 0 || rows() != "" {
		t.Fatalf("bad CSRF = %d, rpc %v, audit %q: want 403 and no effect", code, f.rpc.logouts, rows())
	}
	f.audit.setFail(true)
	if code, _ := f.unlinkPost(sess, csrf, "default"); code != http.StatusServiceUnavailable || len(f.rpc.logouts) != 0 {
		t.Fatalf("intent failure = %d, rpc %v: want 503 and no RPC", code, f.rpc.logouts)
	}
	f.audit.setFail(false)

	f.rpc.logoutFail = true
	if code, body := f.unlinkPost(sess, csrf, "default"); code != http.StatusInternalServerError || strings.Contains(body, "Unlinked.") || !strings.Contains(body, "wa pair --reset") {
		t.Fatalf("failed unlink = %d %q: want 500, no 'Unlinked.', the reset hint", code, body)
	}
	if got := rows(); got != "intent,failed" {
		t.Fatalf("failed unlink audit = %q, want intent,failed", got)
	}

	f.rpc.logoutFail = false
	f.rpc.onLogout = func() {
		if got := f.audit.decisions(domain.AuditUnlink); len(got) == 0 || got[len(got)-1] != "intent" {
			t.Errorf("the RPC ran before its intent row: %v", got)
		}
		f.audit.setFail(true) // the outcome append will fail
	}
	if code, _ := f.unlinkPost(sess, csrf, "default"); code != http.StatusServiceUnavailable {
		t.Fatalf("outcome failure after the effect = %d, want 503 audit_incomplete", code)
	}
	f.audit.setFail(false)
	f.rpc.onLogout = nil
	if code, _ := f.unlinkPost(sess, csrf, "default"); code != http.StatusOK || !strings.HasSuffix(rows(), "intent,ok") {
		t.Fatalf("clean unlink = %d, audit %q", code, rows())
	}
}

// TestAdminLogout_CancelsPairingFlow: an unlink ends a live pairing flow
// first (cause unlink), so no QR outlives the session it would replace.
func TestAdminLogout_CancelsPairingFlow(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		f.rpc.onLogout = func() { // the flow must already be over when session.logout runs
			if st, _ := f.flow(); st != pairCancelled {
				t.Errorf("pair flow is %v when session.logout starts, want cancelled first", st)
			}
		}
		if code, _ := f.unlinkPost(sess, csrf, "default"); code != http.StatusOK {
			t.Fatalf("unlink = %d", code)
		}
		f.wantCancelled(t, "unlink")
	})
}

// logoutCounter is the daemon's SessionTerminator, counting Logout calls.
type logoutCounter struct {
	app.SessionTerminator
	n atomic.Int32
}

func (c *logoutCounter) Logout(context.Context) error { c.n.Add(1); return nil }

// TestAdminLogout_DoubleSubmitIsDeduplicated: the page's key must be the one
// the REAL dispatcher reads, so a double submit runs one Logout and the
// second answer is the first one's replay, never a false failure.
func TestAdminLogout_DoubleSubmitIsDeduplicated(t *testing.T) {
	f := newFixture(t)
	term := &logoutCounter{}
	ad := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: ad, Events: ad, Contacts: ad, Groups: ad, Session: ad, Allowlist: ad, Audit: ad, History: ad, Pairer: ad,
		Idempotency: memory.NewIdempotencyStore(), SessionTerm: term, Profile: "default", SessionCreated: time.Now(),
	})
	t.Cleanup(func() { _ = d.Close() })
	f.s.cfg.RPC = d
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	for i := range 2 {
		if code, body := f.unlinkPost(sess, csrf, "default"); code != http.StatusOK || !strings.Contains(body, "Unlinked.") {
			t.Fatalf("submit %d = %d %q", i+1, code, body)
		}
	}
	if n := term.n.Load(); n != 1 {
		t.Fatalf("Logout ran %d times for one page key, want 1", n)
	}
}

// failOn refuses to record one audit action, as a log that fills mid-request.
type failOn struct {
	*fakeAudit
	act domain.AuditAction
}

func (a failOn) Record(ctx context.Context, e domain.AuditEvent) error {
	if e.Action == a.act {
		return errors.New("disk full")
	}
	return a.fakeAudit.Record(ctx, e)
}

// TestAdminLogout_UnrecordedPairCancelIsShown: if pair_cancel cannot be
// written, the unlink still happens but the response says the record is
// incomplete and /admin shows the audit_incomplete latch.
func TestAdminLogout_UnrecordedPairCancelIsShown(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		f.s.cfg.Audit = failOn{f.audit, domain.AuditPairCancel}
		code, body := f.unlinkPost(sess, csrf, "default")
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "audit_incomplete") || len(f.rpc.logouts) != 1 {
			t.Fatalf("unlink = %d %q, logouts %d; want 503 audit_incomplete after one logout", code, body, len(f.rpc.logouts))
		}
		if home := f.do(req{path: "/admin", cookies: []*http.Cookie{sess}}).Body.String(); !strings.Contains(home, "audit_incomplete") {
			t.Errorf("/admin lacks the audit_incomplete latch: %q", home)
		}
	})
}
