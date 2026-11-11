package admin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const qrSecret = "2@PAIR-SECRET-REF,abc123,def456"

// fakePairer stands in for the daemon's executor-backed pair job: Pair
// blocks until its ctx ends or the test hands it a result.
type fakePairer struct {
	mu        sync.Mutex
	paired    bool
	ignoreCtx bool // a worker that never looks at its ctx
	calls     []string
	ctx       context.Context
	onCall    func()
	result    chan error
}

func (p *fakePairer) Paired(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paired, nil
}

func (p *fakePairer) Pair(ctx context.Context, tokenID string) error {
	p.mu.Lock()
	p.calls, p.ctx = append(p.calls, tokenID), ctx
	on, done := p.onCall, ctx.Done()
	if p.ignoreCtx {
		done = nil
	}
	p.mu.Unlock()
	if on != nil {
		on()
	}
	select {
	case <-done:
		return ctx.Err()
	case err := <-p.result:
		return err
	}
}

func (p *fakePairer) n() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) }

func (p *fakePairer) jobCtx() context.Context { p.mu.Lock(); defer p.mu.Unlock(); return p.ctx }

// emit plays a whatsmeow QR code event into the running job's sink.
func (p *fakePairer) emit(t *testing.T, code string) {
	t.Helper()
	ctx := p.jobCtx()
	if ctx == nil || app.QRSinkFrom(ctx) == nil {
		t.Fatal("no pair job, or its ctx carries no QR sink")
	}
	sink := app.QRSinkFrom(ctx)
	sink(code)
}

type pairFixture struct {
	*fixture
	p *fakePairer
}

// pairTest runs fn in a synctest bubble with a pair-enabled admin server
// and shuts it down afterwards, which cancels any flow left running.
func pairTest(t *testing.T, fn func(t *testing.T, f *pairFixture)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		f := &pairFixture{fixture: newFixture(t), p: &fakePairer{result: make(chan error, 1)}}
		f.s.cfg.Pairer = f.p
		f.h = f.s.Handler()
		defer func() { _ = f.s.Shutdown(context.Background()); synctest.Wait() }()
		fn(t, f)
	})
}

func (f *pairFixture) session(t *testing.T, name string, ttl time.Duration) (*http.Cookie, string, sqlitetokens.Token) {
	t.Helper()
	tok := f.issue(t, name, sqlitetokens.ScopeAdmin, ttl)
	sess, csrf := f.login(t, tok.Raw)
	return sess, csrf, tok
}

func (f *pairFixture) post(path string, sess *http.Cookie, csrf string) *httptest.ResponseRecorder {
	rec := f.do(req{method: http.MethodPost, path: path, form: url.Values{"csrf": {csrf}}, cookies: []*http.Cookie{sess}})
	synctest.Wait()
	return rec
}

func (f *pairFixture) page(sess *http.Cookie) *httptest.ResponseRecorder {
	return f.do(req{path: "/admin/pair", cookies: []*http.Cookie{sess}})
}

var qrSrcRE = regexp.MustCompile(`src="(/admin/pair/[A-Za-z0-9_-]+/qr\.png)"`)

// qrURL loads the pair page and returns its QR image URL, "" if none.
func (f *pairFixture) qrURL(sess *http.Cookie) string {
	m := qrSrcRE.FindStringSubmatch(f.page(sess).Body.String())
	if m == nil {
		return ""
	}
	return m[1]
}

func (f *pairFixture) fetch(path string, sess *http.Cookie) *httptest.ResponseRecorder {
	return f.do(req{path: path, cookies: []*http.Cookie{sess}})
}

// showing starts a flow for a new session and plays one code into it.
func (f *pairFixture) showing(t *testing.T, ttl time.Duration) (*http.Cookie, string, sqlitetokens.Token) {
	t.Helper()
	sess, csrf, tok := f.session(t, "ops", ttl)
	if rec := f.post("/admin/pair/start", sess, csrf); rec.Code != http.StatusSeeOther {
		t.Fatalf("pair start = %d %q", rec.Code, rec.Body.String())
	}
	f.p.emit(t, qrSecret)
	return sess, csrf, tok
}

func (f *pairFixture) flow() (pairState, string) {
	f.s.pair.mu.Lock()
	defer f.s.pair.mu.Unlock()
	return f.s.pair.state, f.s.pair.cause
}

func (f *pairFixture) wantCancelled(t *testing.T, cause string) {
	t.Helper()
	if st, c := f.flow(); st != pairCancelled || c != cause {
		t.Fatalf("flow %s (cause %q), want cancelled by %q", st, c, cause)
	}
	if ctx := f.p.jobCtx(); ctx == nil || ctx.Err() == nil {
		t.Error("the pair job's ctx is still live")
	}
	if got := f.audit.details(domain.AuditPairCancel); len(got) != 1 || got[0] != cause {
		t.Errorf("pair_cancel rows %v, want one with cause %q", got, cause)
	}
}

func (a *fakeAudit) details(act domain.AuditAction) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.Action == act {
			out = append(out, e.Detail)
		}
	}
	return out
}

func TestAdminPair_StartRequiresCSRF(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.session(t, "ops", 0)
		for _, bad := range []string{"", "stale-" + csrf} {
			if rec := f.post("/admin/pair/start", sess, bad); rec.Code != http.StatusForbidden {
				t.Errorf("start with csrf %q = %d, want 403", bad, rec.Code)
			}
		}
		if f.p.n() != 0 || len(f.audit.decisions(domain.AuditPairStart)) != 0 {
			t.Fatalf("a forged start reached the pairer (%d calls) or the audit log", f.p.n())
		}
		f.post("/admin/pair/start", sess, csrf)
		f.p.emit(t, qrSecret)
		if rec := f.post("/admin/pair/cancel", sess, ""); rec.Code != http.StatusForbidden {
			t.Errorf("cancel without csrf = %d, want 403", rec.Code)
		}
		if st, _ := f.flow(); st != pairShowing {
			t.Errorf("a forged cancel ended the flow: %s", st)
		}
	})
}

func TestAdminPair_StartAudited(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, tok := f.session(t, "ops", 0)
		var atCall []string
		f.p.onCall = func() { atCall = f.audit.decisions(domain.AuditPairStart) }
		if rec := f.post("/admin/pair/start", sess, csrf); rec.Code != http.StatusSeeOther {
			t.Fatalf("start = %d", rec.Code)
		}
		if f.p.n() != 1 || f.p.calls[0] != tok.ID {
			t.Fatalf("pair job calls %v, want one as token %s", f.p.calls, tok.ID)
		}
		if len(atCall) == 0 || atCall[0] != "intent" {
			t.Errorf("audit when the job ran: %v, want the intent first", atCall)
		}
		if got := strings.Join(f.audit.decisions(domain.AuditPairStart), ","); got != "intent,ok" {
			t.Errorf("pair_start rows %q, want intent,ok", got)
		}
	})
}

func TestAdminPair_AuditFailureFailsClosed(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.session(t, "ops", 0)
		f.audit.setFail(true)
		if rec := f.post("/admin/pair/start", sess, csrf); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("start with the audit log down = %d, want 503", rec.Code)
		}
		if st, _ := f.flow(); f.p.n() != 0 || st != pairIdle {
			t.Fatalf("pairing started without its audit intent: %d calls, %s", f.p.n(), st)
		}
		f.audit.setFail(false)
		f.post("/admin/pair/start", sess, csrf)
		f.audit.setFail(true)
		if rec := f.post("/admin/pair/cancel", sess, csrf); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("cancel with the audit log down = %d, want 503 audit_incomplete", rec.Code)
		}
		if st, _ := f.flow(); st != pairCancelled {
			t.Errorf("an audit failure kept the flow alive: %s", st)
		}
		f.audit.setFail(false)
		if !strings.Contains(f.do(req{path: "/admin", cookies: []*http.Cookie{sess}}).Body.String(), "audit_incomplete") {
			t.Error("/admin does not show audit_incomplete")
		}
	})
}

func TestAdminPair_AlreadyPaired(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.session(t, "ops", 0)
		f.p.paired = true
		body := f.page(sess).Body.String()
		if !strings.Contains(body, "already paired") || strings.Contains(body, `action="/admin/pair/start"`) {
			t.Fatalf("paired daemon page: %q", body)
		}
		if rec := f.post("/admin/pair/start", sess, csrf); rec.Code != http.StatusConflict || f.p.n() != 0 {
			t.Errorf("start on a paired daemon = %d with %d pair calls, want 409 and none", rec.Code, f.p.n())
		}
	})
}

func TestAdminPair_SingleViewer(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		a, _, _ := f.showing(t, 0)
		b, bcsrf, _ := f.session(t, "other", 0)
		body := f.page(b).Body.String()
		if !strings.Contains(body, "pairing in progress") || qrSrcRE.MatchString(body) {
			t.Fatalf("second session's page: %q", body)
		}
		if u := f.qrURL(a); u == "" || f.fetch(u, b).Code != http.StatusNotFound {
			t.Error("another session fetched the owner's QR")
		}
		if rec := f.post("/admin/pair/start", b, bcsrf); rec.Code != http.StatusConflict {
			t.Errorf("second start = %d, want 409", rec.Code)
		}
		f.post("/admin/pair/cancel", b, bcsrf)
		if st, _ := f.flow(); st != pairShowing || f.p.n() != 1 {
			t.Errorf("another session ended or restarted the flow: %s, %d jobs", st, f.p.n())
		}
	})
}

func TestAdminPair_MetaRefreshCadence(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, tok := f.session(t, "ops", 0)
		f.post("/admin/pair/start", sess, csrf)
		const refresh = `<meta http-equiv="refresh" content="5">`
		if body := f.page(sess).Body.String(); !strings.Contains(body, refresh) || strings.Contains(body, "<script") {
			t.Errorf("starting page lacks the 5 s refresh: %q", body)
		}
		f.p.emit(t, qrSecret)
		rec := f.page(sess)
		if !strings.Contains(rec.Body.String(), refresh) || strings.Contains(rec.Body.String(), "<script") {
			t.Errorf("QR page lacks the 5 s refresh: %q", rec.Body.String())
		}
		if u1, u2 := f.qrURL(sess), f.qrURL(sess); u1 == "" || u1 == u2 {
			t.Errorf("each refresh must mint a fresh nonce: %q %q", u1, u2)
		}
		if err := f.other.Revoke(t.Context(), tok.ID); err != nil {
			t.Fatal(err)
		}
		if u := f.qrURL(sess); u != "" {
			t.Errorf("a refresh after revocation minted %q", u)
		}
		f.wantCancelled(t, "token_gone")
	})
}

func TestAdminPair_NonceSingleUse(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, _, _ := f.showing(t, 0)
		u := f.qrURL(sess)
		rec := f.fetch(u, sess)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
			t.Fatalf("first fetch = %d %q", rec.Code, rec.Header().Get("Content-Type"))
		}
		if f.fetch(u, sess).Code != http.StatusNotFound {
			t.Error("nonce served twice")
		}
		u = f.qrURL(sess)
		<-time.After(pairNonceTTL)
		if f.fetch(u, sess).Code != http.StatusNotFound {
			t.Error("a 60 s old nonce was served")
		}
	})
}

func TestAdminPair_QRNotPersisted(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		page := f.page(sess)
		img := f.fetch(qrSrcRE.FindStringSubmatch(page.Body.String())[1], sess)
		for name, rec := range map[string]*httptest.ResponseRecorder{"page": page, "image": img} {
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("%s Cache-Control %q, want no-store", name, cc)
			}
			for k, v := range rec.Header() {
				if strings.Contains(strings.Join(v, " "), qrSecret) {
					t.Errorf("%s header %s carries the QR payload", name, k)
				}
			}
		}
		if strings.Contains(page.Body.String(), qrSecret) {
			t.Error("the page embeds the QR payload; only the nonce URL may appear")
		}
		f.post("/admin/pair/cancel", sess, csrf)
		f.audit.mu.Lock()
		for _, e := range f.audit.events {
			if strings.Contains(e.Detail+e.Decision+e.Actor, qrSecret) {
				t.Errorf("audit row %s carries the QR payload", e.Action)
			}
		}
		f.audit.mu.Unlock()
		if strings.Contains(f.logs.String(), qrSecret) {
			t.Error("the QR payload reached the log")
		}
	})
}

func TestAdminPair_ExplicitCancel(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		u := f.qrURL(sess)
		if rec := f.post("/admin/pair/cancel", sess, csrf); rec.Code != http.StatusSeeOther {
			t.Fatalf("cancel = %d", rec.Code)
		}
		f.wantCancelled(t, "explicit")
		if f.fetch(u, sess).Code != http.StatusNotFound {
			t.Error("QR served after cancel")
		}
		if body := f.page(sess).Body.String(); !strings.Contains(body, "cancelled") || !strings.Contains(body, `action="/admin/pair/start"`) {
			t.Errorf("page after cancel: %q", body)
		}
	})
}

func TestAdminPair_CancelledOnSessionLogout(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		f.post("/admin/logout", sess, csrf)
		f.wantCancelled(t, "session_logout")
	})
}

func (f *pairFixture) sweeping(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); f.s.runSweeper(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestAdminPair_CancelledOnSessionExpiry(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		f.sweeping(t)
		sess, csrf, _ := f.session(t, "ops", 0)
		for range int(absoluteTTL/(10*time.Minute)) - 1 {
			<-time.After(10 * time.Minute)
			f.signedIn(sess)
		}
		<-time.After(10*time.Minute - pairWindow/2)
		f.post("/admin/pair/start", sess, csrf)
		f.p.emit(t, qrSecret)
		<-time.After(pairWindow/2 + SweepInterval + time.Second) // a sweep past the absolute limit, no request
		synctest.Wait()
		f.wantCancelled(t, "session_expired")
	})
}

func TestAdminPair_CancelledOnTokenRevoke(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		f.sweeping(t)
		_, _, tok := f.showing(t, 0)
		if err := f.other.Revoke(t.Context(), tok.ID); err != nil {
			t.Fatal(err)
		}
		<-time.After(SweepInterval)
		synctest.Wait()
		f.wantCancelled(t, "token_gone")
	})
}

func TestAdminPair_CancelledOnTokenRevokeFromUI(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		_, _, tok := f.showing(t, 0)
		b, bcsrf, _ := f.session(t, "other", 0)
		f.tokenPost(b, "revoke", url.Values{"id": {tok.ID}, "csrf": {bcsrf}})
		f.wantCancelled(t, "token_revoked")
	})
}

func TestAdminPair_CancelledOnTokenExpiry(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, _, _ := f.showing(t, time.Minute)
		u := f.qrURL(sess)
		<-time.After(time.Minute + 2*time.Second)
		if f.fetch(u, sess).Code != http.StatusNotFound {
			t.Error("QR served after the backing token expired")
		}
		f.wantCancelled(t, "token_gone")
		f.p.emit(t, "late-"+qrSecret)
		if st, _ := f.flow(); st != pairCancelled {
			t.Errorf("a late code revived the flow: %s", st)
		}
	})
}

func TestAdminPair_CancelledOnTimeout(t *testing.T) {
	for _, wedged := range []bool{false, true} {
		pairTest(t, func(t *testing.T, f *pairFixture) {
			f.p.ignoreCtx = wedged
			sess, _, _ := f.showing(t, 0)
			<-time.After(pairWindow - time.Second)
			if st, _ := f.flow(); st != pairShowing {
				t.Fatalf("flow ended before its window: %s", st)
			}
			<-time.After(time.Second)
			synctest.Wait()
			f.wantCancelled(t, "timeout")
			if f.qrURL(sess) != "" {
				t.Error("a timed-out flow still shows a QR")
			}
			if wedged {
				f.p.result <- errors.New("finally returned")
			}
		})
	}
}

func TestAdminPair_CancelledOnDaemonShutdown(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, _, _ := f.showing(t, 0)
		u := f.qrURL(sess)
		if err := f.s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		f.wantCancelled(t, "shutdown")
		f.p.emit(t, "late-"+qrSecret)
		if f.fetch(u, sess).Code != http.StatusNotFound || f.qrURL(sess) != "" {
			t.Error("QR served after shutdown")
		}
	})
}

func TestAdminPair_QRFetchAfterCancelIs404(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		old := f.qrURL(sess)
		f.post("/admin/pair/cancel", sess, csrf)
		if rec := f.fetch(old, sess); rec.Code != http.StatusNotFound {
			t.Errorf("fetch after cancel = %d, want 404", rec.Code)
		}
		f.post("/admin/pair/start", sess, csrf) // a new generation
		f.p.emit(t, "new-"+qrSecret)
		if rec := f.fetch(old, sess); rec.Code != http.StatusNotFound {
			t.Errorf("an old generation's nonce = %d, want 404", rec.Code)
		}
	})
}

func TestAdminPair_DirectStoreRevokeDeniesEveryQRRequest(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, _, tok := f.showing(t, 0)
		urls := []string{f.qrURL(sess), f.qrURL(sess), f.qrURL(sess)}
		if err := f.other.Revoke(t.Context(), tok.ID); err != nil {
			t.Fatal(err)
		}
		for i, u := range urls {
			if rec := f.fetch(u, sess); rec.Code != http.StatusNotFound {
				t.Errorf("QR request %d after a direct store revoke = %d, want 404", i, rec.Code)
			}
		}
		f.wantCancelled(t, "token_gone")
	})
}

func TestAdminPair_SuccessAfterCancelAudited(t *testing.T) {
	pairTest(t, func(t *testing.T, f *pairFixture) {
		sess, csrf, _ := f.showing(t, 0)
		f.post("/admin/pair/cancel", sess, csrf)
		f.s.PairSucceeded() // whatsmeow's PairSuccess after the cancel
		f.s.PairSucceeded()
		if n := len(f.audit.decisions(domain.AuditPairSuccessAfterCancel)); n != 1 {
			t.Fatalf("%d pair_success_after_cancel rows, want 1", n)
		}
		if body := f.do(req{path: "/admin", cookies: []*http.Cookie{sess}}).Body.String(); !strings.Contains(body, "linked after it was cancelled") {
			t.Errorf("/admin lacks the success-after-cancel banner: %q", body)
		}

		f.p.ignoreCtx = true // the job itself reports success past a cancel
		f.post("/admin/pair/start", sess, csrf)
		f.post("/admin/pair/cancel", sess, csrf)
		f.p.result <- nil
		synctest.Wait()
		if n := len(f.audit.decisions(domain.AuditPairSuccessAfterCancel)); n != 2 {
			t.Errorf("%d pair_success_after_cancel rows after the job's late success, want 2", n)
		}
	})
}
