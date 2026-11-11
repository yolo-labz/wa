package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/pairhtml"
)

// Pairer is the daemon's pair method as the admin UI drives it. Paired
// reports whether a device is linked. Pair blocks until the pairing ends;
// its ctx carries the flow's QR sink (app.WithQRSink) and ending that ctx
// cancels the pairing. The daemon runs it through Executor.Run as
// tokenID's TokenPrincipal (spec 116 ruling D2).
type Pairer interface {
	Paired(ctx context.Context) (bool, error)
	Pair(ctx context.Context, tokenID string) error
}

const (
	pairWindow  = 3 * time.Minute // SR-116-06: a flow's whole life
	pairRefresh = 5               // pair page meta refresh, seconds (pair.html says it too)
)

var errPairAudit = errors.New("admin: pairing audit record failed")

// pairRoutes adds FR-116-02's routes; without a Pairer they stay absent.
func (s *Server) pairRoutes(mux *http.ServeMux) {
	if s.cfg.Pairer == nil {
		return
	}
	mux.HandleFunc("GET /admin/pair", s.authed(s.pairPage))
	mux.HandleFunc("POST /admin/pair/start", s.authed(s.pairStart))
	mux.HandleFunc("POST /admin/pair/cancel", s.authed(s.pairCancel))
	mux.HandleFunc("GET /admin/pair/{nonce}/qr.png", s.pairImage)
}

// recordPair is the flow's audit hook; a failure latches audit_incomplete.
func (s *Server) recordPair(act domain.AuditAction, actor, cause string) error {
	if !s.outcome(context.Background(), act, actor, domain.JID{}, "ok", cause) {
		return errPairAudit
	}
	return nil
}

// pairPage renders the flow as this session sees it. Each load of a
// showing flow revalidates the session (authed) and mints a fresh
// single-use QR URL; a second session never sees the QR.
func (s *Server) pairPage(w http.ResponseWriter, r *http.Request, sess *session, id string) {
	if n, ok := s.pair.mint(id); ok {
		page, err := pairhtml.RenderStrict(pairhtml.StateQR, pairhtml.Strict{
			Stylesheet: "/admin/static/admin.css", QRURL: "/admin/pair/" + n + "/qr.png", Refresh: pairRefresh,
			CancelURL: "/admin/pair/cancel", CSRF: sess.csrf,
		})
		if err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
		return
	}
	st, cause, mine := s.pair.view(id)
	live := st == pairStarting || st == pairShowing
	paired, err := s.cfg.Pairer.Paired(r.Context())
	ended := ""
	if mine && !live && st != pairIdle {
		ended = string(st)
	}
	s.render(w, http.StatusOK, "pair.html", map[string]any{
		"Profile": s.cfg.Profile, "CSRF": sess.csrf, "Starting": live && mine, "Busy": live && !mine,
		"Paired": paired && !live, "Unknown": err != nil, "Ended": ended, "Cause": cause,
	})
}

// pairStart checks the daemon is unpaired, records the intent (fail
// closed), then starts the flow's pair job with a 3 min window.
func (s *Server) pairStart(w http.ResponseWriter, r *http.Request, sess *session, id string) {
	if !csrfOK(w, r, sess) {
		return
	}
	ctx := r.Context()
	if paired, err := s.cfg.Pairer.Paired(ctx); err != nil || paired {
		code := http.StatusConflict
		if err != nil {
			code = http.StatusServiceUnavailable
		}
		http.Error(w, "already paired, or the pairing state is unavailable; nothing was started", code)
		return
	}
	if !s.intent(ctx, w, domain.AuditPairStart, sess.tokenID, domain.JID{}) {
		return
	}
	pctx, stop := context.WithTimeout(context.Background(), pairWindow)
	gen, sink, err := s.pair.begin(id, sess.tokenID, stop)
	if err != nil {
		stop()
		s.outcome(ctx, domain.AuditPairStart, sess.tokenID, domain.JID{}, "refused", "busy")
		http.Error(w, "a pairing is already in progress", http.StatusConflict)
		return
	}
	timedOut := func(_ string, g uint64) bool { return g == gen && errors.Is(pctx.Err(), context.DeadlineExceeded) }
	context.AfterFunc(pctx, func() { _ = s.pair.cancelIf(timedOut, "timeout") }) // also ends a job that ignores its ctx
	go func() {
		err := s.cfg.Pairer.Pair(app.WithQRSink(pctx, sink), sess.tokenID)
		_ = s.pair.cancelIf(timedOut, "timeout") // a timed-out job ends cancelled, not failed
		if s.pair.end(gen, err) {
			s.bannerPair()
		}
	}()
	if !s.outcome(ctx, domain.AuditPairStart, sess.tokenID, domain.JID{}, "ok", "") {
		http.Error(w, "audit_incomplete: pairing started, but the record of it failed", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/admin/pair", http.StatusSeeOther)
}

// pairCancel is the explicit trigger; only the owning session cancels.
// A failed pair_cancel record still leaves the flow cancelled.
func (s *Server) pairCancel(w http.ResponseWriter, r *http.Request, sess *session, id string) {
	if !csrfOK(w, r, sess) {
		return
	}
	if err := s.pair.cancelIf(ownedBy(id), "explicit"); err != nil {
		http.Error(w, "audit_incomplete: pairing cancelled, but the record of it failed", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/admin/pair", http.StatusSeeOther)
}

// pairImage serves the QR behind a nonce once. Every refusal, including
// a signed-out, expired or revoked session, is the same 404. The PNG is
// encoded per request into a buffer zeroed after the write; nothing is
// cached.
func (s *Server) pairImage(w http.ResponseWriter, r *http.Request) {
	var code []byte
	ok := false
	if sess, id := s.session(r); sess != nil {
		code, ok = s.pair.take(id, r.PathValue("nonce"))
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(string(code), qrcode.Medium, 320)
	clear(code)
	if err != nil {
		http.Error(w, "QR encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
	clear(png)
}

// PairSucceeded is the daemon's PairSuccess observer. A success for a
// cancelled flow cannot be undone; it is recorded and bannered on /admin.
func (s *Server) PairSucceeded() {
	if s.pair.succeeded() {
		s.bannerPair()
	}
}

func (s *Server) bannerPair() {
	s.mu.Lock()
	s.pairLate = true
	s.mu.Unlock()
}
