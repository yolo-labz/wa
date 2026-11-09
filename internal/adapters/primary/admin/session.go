package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	cookieSession = "__Host-wa_admin"
	cookiePre     = "__Host-wa_admin_pre"
	preTTL        = 10 * time.Minute
	idleTTL       = 15 * time.Minute
	absoluteTTL   = 8 * time.Hour
	loginLimit    = 10
	loginWindow   = 15 * time.Minute
	scopeAdmin    = "admin"
)

// session is server-side state behind the __Host-wa_admin cookie. It
// holds the backing token's id, never its secret.
type session struct {
	tokenID, tokenName, csrf string
	created, seen            time.Time
}

// randomID is 256 bits from crypto/rand, base64url without padding.
func randomID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error (Go 1.24+)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) mac(v string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

// setCookie writes a __Host- cookie: Secure, host-only, Path=/,
// HttpOnly, SameSite=Strict. maxAge 0 is a browser-session cookie,
// negative deletes.
func setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	c := &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
	if maxAge < 0 {
		c.MaxAge = -1
	} else if maxAge > 0 {
		c.MaxAge = int(maxAge / time.Second)
	}
	http.SetCookie(w, c)
}

// renderLogin issues a fresh pre-login id (SR-116-03): the cookie carries
// "<random>.<issued-unix>", the form carries its HMAC under the
// per-process key, and POST /admin/login requires both within 10 min.
func (s *Server) renderLogin(w http.ResponseWriter, status int, msg string) {
	pre := randomID() + "." + strconv.FormatInt(time.Now().Unix(), 10)
	setCookie(w, cookiePre, pre, preTTL)
	s.render(w, status, "login.html", map[string]any{"Profile": s.cfg.Profile, "CSRF": s.mac(pre), "Error": msg})
}

func (s *Server) preValid(r *http.Request) bool {
	c, err := r.Cookie(cookiePre)
	if err != nil {
		return false
	}
	_, ts, _ := strings.Cut(c.Value, ".")
	issued, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(issued, 0)) > preTTL {
		return false
	}
	return hmac.Equal([]byte(r.PostFormValue("csrf")), []byte(s.mac(c.Value)))
}

// reserve is the login limiter (SR-116-07). Every peer is loopback, so the
// limit is effectively per daemon; forwarding headers are never consulted.
// The window check and the slot claim happen under one lock, so a parallel
// burst cannot pass the check together. A claim that does not end as a
// failed login is returned by release.
func (s *Server) reserve() (stamp time.Time, wait time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.failures = slices.DeleteFunc(s.failures, func(t time.Time) bool { return now.Sub(t) >= loginWindow })
	if len(s.failures) >= loginLimit {
		return time.Time{}, loginWindow - now.Sub(s.failures[0])
	}
	s.failures = append(s.failures, now)
	return now, 0
}

func (s *Server) release(stamp time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.Index(s.failures, stamp); i >= 0 {
		s.failures = slices.Delete(s.failures, i, i+1)
	}
}

// login is FR-116-07: an admin-scope token becomes a server-side session.
// A wrong, read or send token gets the same page and status, so the form
// is not a scope oracle.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	stamp, wait := s.reserve()
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		http.Error(w, "too many failed logins; existing sessions and the wad token CLI still work", http.StatusTooManyRequests)
		return
	}
	failed := false
	defer func() {
		if !failed {
			s.release(stamp)
		}
	}()
	if !s.preValid(r) {
		http.Error(w, "login form expired or forged; reload the login page", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	if !s.intent(ctx, w, domain.AuditAdminLogin, "", domain.JID{}) {
		return
	}
	tok, err := s.cfg.Tokens.Verify(ctx, r.PostFormValue("token"))
	if err != nil || tok.Scope != scopeAdmin {
		failed = true
		s.cfg.Log.Warn("admin login refused", "token_id", tok.ID, "err", err)
		s.outcome(ctx, domain.AuditAdminLogin, tok.ID, domain.JID{}, "denied", "")
		s.renderLogin(w, http.StatusUnauthorized, "Invalid admin token.")
		return
	}
	// The id rotates at every login; any id the browser already held is
	// discarded rather than promoted (fixation).
	if old, err := r.Cookie(cookieSession); err == nil {
		s.drop(old.Value)
	}
	id, now := randomID(), time.Now()
	s.mu.Lock()
	s.sessions[id] = &session{tokenID: tok.ID, tokenName: tok.Name, csrf: randomID(), created: now, seen: now}
	s.mu.Unlock()
	if !s.outcome(ctx, domain.AuditAdminLogin, tok.ID, domain.JID{}, "ok", "") {
		s.drop(id)
		http.Error(w, "audit_incomplete: the login could not be recorded, so it was discarded", http.StatusServiceUnavailable)
		return
	}
	setCookie(w, cookieSession, id, 0)
	setCookie(w, cookiePre, "", -1)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// csrfOK checks the session's synchronizer token (SR-116-03) and
// answers 403 when it is missing or stale.
func csrfOK(w http.ResponseWriter, r *http.Request, sess *session) bool {
	if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(sess.csrf)) != 1 {
		http.Error(w, "missing or stale form token; reload the page", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess *session, id string) {
	if !csrfOK(w, r, sess) {
		return
	}
	ctx := r.Context()
	if !s.intent(ctx, w, domain.AuditAdminLogout, sess.tokenID, domain.JID{}) {
		return
	}
	s.drop(id)
	setCookie(w, cookieSession, "", -1)
	if !s.outcome(ctx, domain.AuditAdminLogout, sess.tokenID, domain.JID{}, "ok", "") {
		http.Error(w, "audit_incomplete: signed out, but the record of it failed", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// authed resolves the session cookie and revalidates it against the
// token store before the handler runs (SR-116-01/04).
func (s *Server) authed(next func(http.ResponseWriter, *http.Request, *session, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sess *session
		c, err := r.Cookie(cookieSession)
		if err == nil {
			sess = s.validate(r.Context(), c.Value)
		}
		if sess == nil {
			if r.Method == http.MethodPost {
				http.Error(w, "not signed in", http.StatusForbidden)
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r, sess, c.Value)
	}
}

func (s *Server) validate(ctx context.Context, id string) *session {
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	if !s.alive(ctx, sess) {
		s.drop(id)
		return nil
	}
	s.mu.Lock()
	sess.seen = time.Now()
	s.mu.Unlock()
	return sess
}

// alive applies idle/absolute expiry, then reads the backing token from
// the store. Revoked, expired, missing, downgraded and store errors all
// fail closed; a login-time result is never trusted.
func (s *Server) alive(ctx context.Context, sess *session) bool {
	now := time.Now()
	s.mu.Lock()
	expired := now.Sub(sess.seen) > idleTTL || now.Sub(sess.created) > absoluteTTL
	s.mu.Unlock()
	if expired {
		return false
	}
	tok, err := s.cfg.Tokens.Lookup(ctx, sess.tokenID)
	if err != nil {
		s.cfg.Log.Info("admin session dropped", "token_id", sess.tokenID, "err", err)
		return false
	}
	return tok.Scope == scopeAdmin
}

// dropToken ends every session backed by tokenID (FR-116-05).
func (s *Server) dropToken(tokenID string) {
	s.mu.Lock()
	maps.DeleteFunc(s.sessions, func(_ string, v *session) bool { return v.tokenID == tokenID })
	s.mu.Unlock()
}

func (s *Server) drop(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// runSweeper revalidates every session each SweepInterval, independent
// of requests, so a token revoked by another process stops backing a
// session within 30 s even if no request arrives.
func (s *Server) runSweeper(ctx context.Context) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			snap := maps.Clone(s.sessions)
			s.mu.Unlock()
			for id, sess := range snap {
				if !s.alive(ctx, sess) {
					s.drop(id)
				}
			}
		}
	}
}

// intent durably appends the audit intent of a requested mutation
// (SR-116-08). On failure it answers 503 and the caller must not
// dispatch. A successful append also proves the audit log recovered,
// which clears the audit_incomplete latch.
func (s *Server) intent(ctx context.Context, w http.ResponseWriter, act domain.AuditAction, actor string, subject domain.JID) bool {
	if err := s.record(ctx, act, actor, subject, "intent", ""); err != nil {
		s.cfg.Log.Error("admin audit intent failed; mutation refused", "action", act.String(), "err", err)
		http.Error(w, "audit log unavailable; nothing was changed", http.StatusServiceUnavailable)
		return false
	}
	s.mu.Lock()
	s.auditIncomplete = false
	s.mu.Unlock()
	return true
}

// outcome appends the result. A failure latches audit_incomplete, shown
// on /admin until a later intent append succeeds; the effect is never
// claimed undone.
func (s *Server) outcome(ctx context.Context, act domain.AuditAction, actor string, subject domain.JID, decision, detail string) bool {
	if err := s.record(ctx, act, actor, subject, decision, detail); err != nil {
		s.cfg.Log.Error("admin audit outcome failed", "action", act.String(), "decision", decision, "err", err)
		s.mu.Lock()
		s.auditIncomplete = true
		s.mu.Unlock()
		return false
	}
	return true
}

func (s *Server) record(ctx context.Context, act domain.AuditAction, actor string, subject domain.JID, decision, detail string) error {
	return s.cfg.Audit.Record(ctx, domain.NewAuditEventFrom("admin", actor, act, subject, decision, detail))
}
