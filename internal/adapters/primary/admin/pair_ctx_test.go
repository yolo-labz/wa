package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ctxPairer gives fakePairer the real adapter's Paired behaviour: a cancelled
// ctx fails the read (whatsmeow session.go:18-20).
type ctxPairer struct{ *fakePairer }

func (c ctxPairer) Paired(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return c.fakePairer.Paired(ctx)
}

// TestAdminPair_LinkedCheckSurvivesRequestCtxCancel: net/http cancels the
// request ctx when the start handler returns, and the pair goroutine
// outlives it. The post-exit Paired() check must not run on that ctx, or it
// fails in production and a link made while the flow was cancelled goes
// unrecorded (p8 6a2-fix2 L1).
func TestAdminPair_LinkedCheckSurvivesRequestCtxCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fx := &pairFixture{fixture: newFixture(t), p: &fakePairer{result: make(chan error, 1)}}
		fx.s.cfg.Pairer = ctxPairer{fx.p}
		fx.h = fx.s.Handler()
		defer func() { _ = fx.s.Shutdown(context.Background()); synctest.Wait() }()
		sess, csrf, _ := fx.session(t, "ops", 0)

		ctx, cancel := context.WithCancel(context.Background())
		hr := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/pair/start", strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
		hr.Host, hr.RemoteAddr = testHost, "127.0.0.1:50000"
		hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		hr.Header.Set("Origin", "http://"+hr.Host)
		hr.AddCookie(sess)
		fx.h.ServeHTTP(httptest.NewRecorder(), hr)
		cancel() // what net/http does when the handler returns
		synctest.Wait()
		fx.p.emit(t, qrSecret)

		fx.p.mu.Lock()
		fx.p.paired = true // the adapter recorded the link; the flow is then cancelled
		fx.p.mu.Unlock()
		fx.post("/admin/pair/cancel", sess, csrf)
		synctest.Wait()
		if n := len(fx.audit.decisions(domain.AuditPairSuccessAfterCancel)); n != 1 {
			t.Errorf("%d pair_success_after_cancel rows, want 1: Paired() did not see the link", n)
		}
	})
}
