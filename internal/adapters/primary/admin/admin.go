// Package admin is the operator web UI (spec 116): a server-rendered
// html/template surface on its own loopback listener (WAD_ADMIN_ADDR),
// never on the REST mux. Network location comes only from the bind
// address and each connection's TCP peer; no request header decides
// where a request came from or which scheme it used (SR-116-02/04).
package admin

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

//go:embed templates/*.html static/admin.css
var assets embed.FS

var tmpl = template.Must(template.ParseFS(assets, "templates/*.html"))

// csp is the spec 116 §3 header set on every admin response.
const csp = "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// SweepInterval bounds how long a revoked or expired backing token can
// keep a session alive without any request arriving (SR-116-04: ≤30 s).
const SweepInterval = 30 * time.Second

// Token is the admin view of a 110d token row. Raw is set only on the
// Issue result; sessions never retain it.
type Token struct {
	ID, Name, Scope                       string
	Created, Expires, LastUsed, RevokedAt time.Time
	Raw                                   string
}

// ErrTokenGone is what TokenStore.Lookup wraps for a token that is revoked,
// expired or unknown. Any other Lookup error is transient: the request is
// refused but the session survives.
var ErrTokenGone = errors.New("admin: token revoked, expired or unknown")

// TokenStore is the 110d token store as the admin UI consumes it.
// Lookup re-reads validity by id and errors for revoked, expired or
// missing tokens, so a revoke from another process is seen at once.
type TokenStore interface {
	Verify(ctx context.Context, raw string) (Token, error)
	Lookup(ctx context.Context, id string) (Token, error)
	List(ctx context.Context) ([]Token, error)
	Issue(ctx context.Context, name, scope string, ttl time.Duration) (Token, error)
	Revoke(ctx context.Context, id string) error
}

// AuditRecord is one audit line as the tail view shows it.
type AuditRecord struct {
	Time                                             time.Time
	Actor, Action, Subject, Decision, Detail, Source string
}

// AuditTail reads the HMAC-chained audit file (FR-116-06). Verify
// returns the chain error, if any; it never hides rows.
type AuditTail interface {
	Tail(n int) ([]AuditRecord, error)
	Verify() error
}

// Dispatcher is the daemon's JSON-RPC method table. The admin UI drives
// the existing handlers (`allow`, …) through it, never a second code path.
type Dispatcher interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

// Config wires one daemon's admin UI.
type Config struct {
	Profile string
	// HTTPSOrigins are exact Host values served by an HTTPS front such
	// as `tailscale serve` (WAD_ADMIN_HTTPS_ORIGINS). Membership alone
	// makes the effective origin https.
	HTTPSOrigins []string
	Tokens       TokenStore
	Audit        app.AuditLog
	RPC          Dispatcher
	AuditTail    AuditTail // nil: the audit view says the log is not file-backed
	Log          *slog.Logger
	Pairer       Pairer       // nil: no pairing routes
	Status       StatusSource // nil: the home page says status is unavailable
}

// Status is the FR-116-01 view, decoded from the daemon's `status`
// payload. The admin package derives nothing: Health, Reasons and
// Unready come from S1's truth table, the same snapshot /readyz reads.
type Status struct {
	domain.StorageHealth
	Health       string   `json:"health"`
	Reasons      []string `json:"reasons"`
	Paired       bool     `json:"paired"`
	Connected    bool     `json:"connected"`
	Account      string   `json:"jid"` // already masked to the last four digits
	Device       uint16   `json:"device"`
	SessionSince int64    `json:"sessionSince"`
	Unready      []string `json:"-"` // the exact /readyz 503 reasons; empty = ready
}

// StatusSource reads one health snapshot.
type StatusSource func(ctx context.Context) (Status, error)

// Server is the admin UI. Sessions live in memory and die with the
// process (SR-116-04).
type Server struct {
	cfg Config
	key []byte // per-process HMAC key for the pre-login CSRF field

	mu              sync.Mutex
	sessions        map[string]*session
	failures        []time.Time // failed logins inside the rate-limit window
	usedPre         map[string]time.Time
	auditIncomplete bool // an outcome append failed (SR-116-08)
	pair            *pairFlow
	pairLate        bool // a pairing succeeded after its cancel (SR-116-06)

	srv    *http.Server
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a Server. It does not listen; see Start.
func New(cfg Config) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	s := &Server{cfg: cfg, key: []byte(randomID()), sessions: map[string]*session{}, usedPre: map[string]time.Time{}}
	s.pair = newPairFlow(time.Now, s.recordPair)
	return s
}

// ValidateAddr enforces SR-116-02 on WAD_ADMIN_ADDR: an IP literal (a
// hostname could move under DNS) in 127.0.0.0/8 or ::1, with an explicit
// port shared with none of others (the REST and health listeners).
func ValidateAddr(addr string, others ...string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("admin addr %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("admin addr %q: must be a loopback IP literal (127.0.0.0/8 or ::1); hostnames, wildcards, LAN, tailnet and public addresses are refused", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("admin addr %q: needs an explicit port 1-65535", addr)
	}
	for _, o := range others {
		if _, p, err := net.SplitHostPort(o); err == nil && p != "" {
			if m, err := strconv.Atoi(p); err == nil && m == n {
				return fmt.Errorf("admin addr %q: port %d is already used by %q", addr, n, o)
			}
		}
	}
	return nil
}

// Handler is the full admin surface behind the network guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/static/admin.css", func(w http.ResponseWriter, _ *http.Request) {
		css, _ := assets.ReadFile("static/admin.css")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(css)
	})
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, _ *http.Request) { s.renderLogin(w, http.StatusOK, "") })
	mux.HandleFunc("POST /admin/login", s.login)
	mux.HandleFunc("GET /admin", s.authed(s.home))
	mux.HandleFunc("POST /admin/logout", s.authed(s.logout))
	mux.HandleFunc("GET /admin/tokens", s.authed(s.tokens))
	mux.HandleFunc("POST /admin/tokens/issue", s.authed(s.issueToken))
	mux.HandleFunc("POST /admin/tokens/revoke", s.authed(s.revokeToken))
	mux.HandleFunc("GET /admin/audit", s.authed(s.auditTail))
	mux.HandleFunc("GET /admin/allowlist", s.authed(s.allowlist))
	mux.HandleFunc("POST /admin/allowlist/add", s.authed(s.allowChange("add", domain.AuditGrant)))
	mux.HandleFunc("POST /admin/allowlist/remove", s.authed(s.allowChange("remove", domain.AuditRevoke)))
	s.pairRoutes(mux)
	return s.guard(mux)
}

// guard applies the response headers, the TCP-peer check (404 for any
// non-loopback peer, whatever its Host), the effective-origin check (403
// for any Host that is neither a loopback origin nor a configured HTTPS
// origin) and, on POST, the Origin / Sec-Fetch-Site check (SR-116-03).
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		if !loopbackPeer(r.RemoteAddr) {
			http.NotFound(w, r)
			return
		}
		origin, ok := s.effectiveOrigin(r.Host)
		if !ok {
			s.render(w, http.StatusForbidden, "reach.html", nil)
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r, origin) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !capBody(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// maxPostBytes bounds every admin POST body. The UI only posts tiny
// url-encoded forms, so a larger body is refused before any parsing.
const maxPostBytes = 64 << 10

// capBody refuses a non-url-encoded POST (415, so multipart never reaches
// the temp-file spill) and buffers at most maxPostBytes, answering 413 past
// that. It runs before the pre-login or session checks: an unauthenticated
// peer must not make the daemon read an unbounded body.
func capBody(w http.ResponseWriter, r *http.Request) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/x-www-form-urlencoded" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPostBytes))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return true
}

func loopbackPeer(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// effectiveOrigin maps Host to the only two origins served (SR-116-04).
// X-Forwarded-Proto, Forwarded and similar headers are never read.
func (s *Server) effectiveOrigin(host string) (string, bool) {
	if h, port, err := net.SplitHostPort(host); err == nil && port != "" {
		switch h {
		case "localhost", "127.0.0.1", "::1":
			return "http://" + host, true
		}
	}
	if host != "" && slices.Contains(s.cfg.HTTPSOrigins, host) {
		return "https://" + host, true
	}
	return "", false
}

func sameOrigin(r *http.Request, origin string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == origin
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.cfg.Log.Error("admin render", "template", name, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
	s.mu.Lock()
	incomplete, late := s.auditIncomplete, s.pairLate
	s.mu.Unlock()
	data := map[string]any{
		"Profile": s.cfg.Profile, "TokenName": sess.tokenName, "CSRF": sess.csrf, "AuditIncomplete": incomplete,
		"Pair": s.cfg.Pairer != nil, "PairLate": late,
	}
	if s.cfg.Status != nil {
		// Same budget as /readyz: the snapshot reads cached state only.
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		st, err := s.cfg.Status(ctx)
		cancel()
		if err != nil {
			s.cfg.Log.Error("admin status", "err", err)
		} else {
			data["Status"], data["Since"] = st, unixTime(st.SessionSince)
		}
	}
	s.render(w, http.StatusOK, "home.html", data)
}

func unixTime(sec int64) string {
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).Local().Format("02/01/2006 15:04")
}

// Start serves ln and runs the revocation sweep until Shutdown.
func (s *Server) Start(ln net.Listener) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	s.srv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.cfg.Log.Error("admin serve", "err", err)
		}
	}()
	go func() { defer close(s.done); s.runSweeper(ctx) }()
}

// Shutdown cancels any pairing flow, then stops the listener and the
// sweeper. Safe before Start.
func (s *Server) Shutdown(ctx context.Context) error {
	_ = s.pair.cancel("shutdown")
	if s.srv == nil {
		return nil
	}
	s.cancel()
	err := s.srv.Shutdown(ctx)
	<-s.done
	return err
}
