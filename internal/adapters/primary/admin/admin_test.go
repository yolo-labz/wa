package admin

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	testHost   = "localhost:8443"
	testOrigin = "http://" + testHost
	httpsHost  = "wa-admin.example.ts.net"
)

// storeShim is the test twin of cmd/wad's adminTokenShim.
type storeShim struct {
	s      *sqlitetokens.Store
	verify atomic.Int32
}

func adminTok(t sqlitetokens.Token) Token {
	return Token{
		ID: t.ID, Name: t.Name, Scope: string(t.Scope), Raw: t.Raw,
		Created: t.CreatedAt, Expires: t.ExpiresAt, LastUsed: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
}

func (h *storeShim) Verify(ctx context.Context, raw string) (Token, error) {
	h.verify.Add(1)
	t, err := h.s.Verify(ctx, raw)
	return adminTok(t), err
}

func (h *storeShim) Lookup(ctx context.Context, id string) (Token, error) {
	t, err := h.s.Lookup(ctx, id)
	return adminTok(t), err
}

func (h *storeShim) List(ctx context.Context) ([]Token, error) {
	l, err := h.s.List(ctx)
	out := make([]Token, 0, len(l))
	for _, t := range l {
		out = append(out, adminTok(t))
	}
	return out, err
}

func (h *storeShim) Issue(ctx context.Context, name, scope string, ttl time.Duration) (Token, error) {
	t, err := h.s.Issue(ctx, name, sqlitetokens.Scope(scope), ttl)
	return adminTok(t), err
}

func (h *storeShim) Revoke(ctx context.Context, id string) error { return h.s.Revoke(ctx, id) }

type fakeAudit struct {
	mu     sync.Mutex
	events []domain.AuditEvent
	fail   bool
}

func (a *fakeAudit) Record(_ context.Context, e domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail {
		return errors.New("disk full")
	}
	a.events = append(a.events, e)
	return nil
}

func (a *fakeAudit) setFail(v bool) { a.mu.Lock(); a.fail = v; a.mu.Unlock() }

func (a *fakeAudit) decisions(act domain.AuditAction) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.Action == act {
			if e.Source != "admin" {
				panic("admin audit row without via admin: " + e.Source)
			}
			out = append(out, e.Decision)
		}
	}
	return out
}

type fixture struct {
	s     *Server
	h     http.Handler
	shim  *storeShim
	other *sqlitetokens.Store // a second handle, as `wad token revoke` would hold
	audit *fakeAudit
	rpc   *fakeRPC
	logs  *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.db")
	a, err := sqlitetokens.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sqlitetokens.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	f := &fixture{shim: &storeShim{s: a}, other: b, audit: &fakeAudit{}, logs: &bytes.Buffer{}}
	log := slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.rpc = &fakeRPC{}
	f.s = New(Config{Profile: "default", HTTPSOrigins: []string{httpsHost}, Tokens: f.shim, Audit: f.audit, RPC: f.rpc, Log: log})
	f.h = f.s.Handler()
	return f
}

func (f *fixture) issue(t *testing.T, name string, scope sqlitetokens.Scope, ttl time.Duration) sqlitetokens.Token {
	t.Helper()
	tok, err := f.other.Issue(t.Context(), name, scope, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type req struct {
	method, path, host, peer string
	form                     url.Values
	cookies                  []*http.Cookie
	hdr                      map[string]string
}

func (f *fixture) do(r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = http.MethodGet
	}
	var body *strings.Reader
	if r.form != nil {
		body = strings.NewReader(r.form.Encode())
	} else {
		body = strings.NewReader("")
	}
	hr := httptest.NewRequestWithContext(context.Background(), r.method, r.path, body)
	hr.Host = testHost
	if r.host != "" {
		hr.Host = r.host
	}
	hr.RemoteAddr = "127.0.0.1:50000"
	if r.peer != "" {
		hr.RemoteAddr = r.peer
	}
	if r.form != nil {
		hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if r.method == http.MethodPost {
		hr.Header.Set("Origin", "http://"+hr.Host)
	}
	for k, v := range r.hdr {
		if v == "" {
			hr.Header.Del(k)
		} else {
			hr.Header.Set(k, v)
		}
	}
	for _, c := range r.cookies {
		hr.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, hr)
	return rec
}

var csrfRE = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func formCSRF(t *testing.T, body string) string {
	t.Helper()
	m := csrfRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf field in %q", body)
	}
	return m[1]
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// loginPage fetches the login form and returns its pre-login cookie and field.
func (f *fixture) loginPage(t *testing.T, host string) (*http.Cookie, string) {
	t.Helper()
	rec := f.do(req{path: "/admin/login", host: host})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/login = %d", rec.Code)
	}
	pre := cookieNamed(rec, cookiePre)
	if pre == nil {
		t.Fatal("no pre-login cookie")
	}
	return pre, formCSRF(t, rec.Body.String())
}

func (f *fixture) postLogin(t *testing.T, raw, host string) *httptest.ResponseRecorder {
	t.Helper()
	pre, csrf := f.loginPage(t, host)
	return f.do(req{
		method: http.MethodPost, path: "/admin/login", host: host,
		form: url.Values{"csrf": {csrf}, "token": {raw}}, cookies: []*http.Cookie{pre},
	})
}

// login signs in and returns the session cookie and the page's form token.
func (f *fixture) login(t *testing.T, raw string) (*http.Cookie, string) {
	t.Helper()
	rec := f.postLogin(t, raw, "")
	sess := cookieNamed(rec, cookieSession)
	if rec.Code != http.StatusSeeOther || sess == nil {
		t.Fatalf("login = %d %q", rec.Code, rec.Body.String())
	}
	home := f.do(req{path: "/admin", cookies: []*http.Cookie{sess}})
	if home.Code != http.StatusOK {
		t.Fatalf("GET /admin = %d", home.Code)
	}
	return sess, formCSRF(t, home.Body.String())
}

func (f *fixture) signedIn(sess *http.Cookie) bool {
	return f.do(req{path: "/admin", cookies: []*http.Cookie{sess}}).Code == http.StatusOK
}

func TestAdmin_PublicRequestWithForgedHostRejected(t *testing.T) {
	f := newFixture(t)
	for _, host := range []string{testHost, "127.0.0.1:8443", httpsHost} {
		for _, path := range []string{"/admin/login", "/admin", "/admin/static/admin.css"} {
			rec := f.do(req{path: path, host: host, peer: "203.0.113.7:4444"})
			if rec.Code != http.StatusNotFound || cookieNamed(rec, cookiePre) != nil {
				t.Errorf("public peer, Host %s %s = %d, want 404 without cookie", host, path, rec.Code)
			}
		}
	}
}

func TestAdmin_NonLoopbackPeerRejected(t *testing.T) {
	f := newFixture(t)
	for _, peer := range []string{"100.64.0.9:1", "[fd7a:115c:a1e0::9]:1", "192.168.1.20:1", "10.0.0.2:1"} {
		rec := f.do(req{path: "/admin/login", peer: peer, hdr: map[string]string{"X-Forwarded-For": "127.0.0.1"}})
		if rec.Code != http.StatusNotFound {
			t.Errorf("peer %s = %d, want 404", peer, rec.Code)
		}
	}
}

func TestAdmin_InsecureNonLoopbackOriginRejected(t *testing.T) {
	f := newFixture(t)
	for _, host := range []string{"192.168.1.20:8443", "unlisted.example.ts.net", "localhost", "evil.example:8443"} {
		rec := f.do(req{path: "/admin/login", host: host})
		if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
			t.Errorf("Host %s = %d cookies %d, want 403 and none", host, rec.Code, len(rec.Result().Cookies()))
		}
		if !strings.Contains(rec.Body.String(), "tailscale serve") || !strings.Contains(rec.Body.String(), "wad admin-tunnel") {
			t.Errorf("403 page must name both reach paths: %q", rec.Body.String())
		}
	}
}

func TestAdmin_SessionCookieEstablishedOverLoopbackHTTP(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.s.Start(ln)
	t.Cleanup(func() { _ = f.s.Shutdown(context.Background()) })
	base := "http://localhost:" + strings.Split(ln.Addr().String(), ":")[1]
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, path string, form url.Values, cookies ...*http.Cookie) *http.Response {
		r, _ := http.NewRequestWithContext(t.Context(), method, base+path, strings.NewReader(form.Encode()))
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", base)
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	page := send(http.MethodGet, "/admin/login", nil)
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(page.Body)
	_ = page.Body.Close()
	resp := send(http.MethodPost, "/admin/login", url.Values{"csrf": {formCSRF(t, buf.String())}, "token": {tok.Raw}}, page.Cookies()...)
	_ = resp.Body.Close()
	var sess *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieSession {
			sess = c
		}
	}
	if resp.StatusCode != http.StatusSeeOther || sess == nil || !sess.Secure {
		t.Fatalf("login over loopback HTTP = %d, cookie %v", resp.StatusCode, sess)
	}
	home := send(http.MethodGet, "/admin", nil, sess)
	_ = home.Body.Close()
	if home.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin with session = %d", home.StatusCode)
	}
}

func TestAdmin_SessionCookieEstablishedViaHTTPSOrigin(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	rec := f.postLogin(t, tok.Raw, httpsHost) // do() sends Origin http://<host>: the wrong scheme
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin http://%s on an HTTPS origin = %d, want 403", httpsHost, rec.Code)
	}
	pre, csrf := f.loginPage(t, httpsHost)
	rec = f.do(req{
		method: http.MethodPost, path: "/admin/login", host: httpsHost, cookies: []*http.Cookie{pre},
		form: url.Values{"csrf": {csrf}, "token": {tok.Raw}}, hdr: map[string]string{"Origin": "https://" + httpsHost},
	})
	sess := cookieNamed(rec, cookieSession)
	if rec.Code != http.StatusSeeOther || sess == nil {
		t.Fatalf("login via HTTPS origin = %d", rec.Code)
	}
	if f.do(req{path: "/admin", host: httpsHost, cookies: []*http.Cookie{sess}}).Code != http.StatusOK {
		t.Fatal("session not accepted on the HTTPS origin")
	}
}

func TestAdmin_SessionCookieAttributes(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	check := func(c *http.Cookie) {
		t.Helper()
		if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
			t.Fatalf("cookie attributes: %+v", c)
		}
	}
	pre, _ := f.loginPage(t, "")
	check(pre)
	if pre.MaxAge != int(preTTL/time.Second) {
		t.Errorf("pre-login MaxAge = %d", pre.MaxAge)
	}
	first, _ := f.login(t, tok.Raw)
	check(first)
	if len(first.Value) != 43 { // 256 bits, base64url
		t.Errorf("session id length %d, want 43", len(first.Value))
	}
	// Re-login while presenting the old id: the id rotates and the old one dies.
	p2, c2 := f.loginPage(t, "")
	rec := f.do(req{
		method: http.MethodPost, path: "/admin/login", cookies: []*http.Cookie{p2, first},
		form: url.Values{"csrf": {c2}, "token": {tok.Raw}},
	})
	second := cookieNamed(rec, cookieSession)
	check(second)
	if second.Value == first.Value || f.signedIn(first) || !f.signedIn(second) {
		t.Fatal("session id must rotate at login and the previous id must stop working")
	}
}

func TestAdmin_ForwardedProtoIgnored(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	spoof := map[string]string{"X-Forwarded-Proto": "https", "Forwarded": "proto=https;host=" + testHost, "X-Forwarded-Host": testHost}
	if rec := f.do(req{path: "/admin/login", host: "evil.example", hdr: spoof}); rec.Code != http.StatusForbidden {
		t.Errorf("forwarding headers must not admit a plain origin: %d", rec.Code)
	}
	pre, csrf := f.loginPage(t, "")
	hdr := map[string]string{"X-Forwarded-Proto": "https", "Origin": "https://" + testHost}
	rec := f.do(req{
		method: http.MethodPost, path: "/admin/login", cookies: []*http.Cookie{pre}, hdr: hdr,
		form: url.Values{"csrf": {csrf}, "token": {tok.Raw}},
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("X-Forwarded-Proto must not upgrade the loopback origin to https: %d", rec.Code)
	}
}

func TestAdmin_RequiresAdminScope(t *testing.T) {
	f := newFixture(t)
	wrong := f.postLogin(t, "not-a-token", "")
	for _, scope := range []sqlitetokens.Scope{sqlitetokens.ScopeRead, sqlitetokens.ScopeSend} {
		rec := f.postLogin(t, f.issue(t, "agent", scope, 0).Raw, "")
		if rec.Code != wrong.Code || cookieNamed(rec, cookieSession) != nil ||
			csrfRE.ReplaceAllString(rec.Body.String(), "") != csrfRE.ReplaceAllString(wrong.Body.String(), "") {
			t.Errorf("%s token: %d, want the wrong-token page (%d) and no session", scope, rec.Code, wrong.Code)
		}
	}
	for _, path := range []string{"/admin", "/admin/nope"} {
		if rec := f.do(req{path: path}); rec.Code != http.StatusSeeOther && rec.Code != http.StatusNotFound {
			t.Errorf("GET %s without a session = %d", path, rec.Code)
		}
	}
	if rec := f.do(req{method: http.MethodPost, path: "/admin/logout", form: url.Values{}}); rec.Code != http.StatusForbidden {
		t.Errorf("POST /admin/logout without a session = %d", rec.Code)
	}
}

func TestAdmin_LoginCSRFRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
		pre, csrf := f.loginPage(t, "")
		other, _ := f.loginPage(t, "")
		good := url.Values{"csrf": {csrf}, "token": {tok.Raw}}
		cases := map[string]req{
			"no cookie":        {form: good},
			"other cookie":     {form: good, cookies: []*http.Cookie{other}},
			"no field":         {form: url.Values{"token": {tok.Raw}}, cookies: []*http.Cookie{pre}},
			"cross origin":     {form: good, cookies: []*http.Cookie{pre}, hdr: map[string]string{"Origin": "http://evil.example"}},
			"no origin or sfs": {form: good, cookies: []*http.Cookie{pre}, hdr: map[string]string{"Origin": ""}},
			"sfs cross-site":   {form: good, cookies: []*http.Cookie{pre}, hdr: map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site"}},
		}
		for name, r := range cases {
			r.method, r.path = http.MethodPost, "/admin/login"
			if rec := f.do(r); rec.Code != http.StatusForbidden || cookieNamed(rec, cookieSession) != nil {
				t.Errorf("%s: %d, want 403 and no session", name, rec.Code)
			}
		}
		<-time.After(preTTL + time.Second)
		if rec := f.do(req{method: http.MethodPost, path: "/admin/login", form: good, cookies: []*http.Cookie{pre}}); rec.Code != http.StatusForbidden {
			t.Errorf("expired pre-login id accepted: %d", rec.Code)
		}
		pre, csrf = f.loginPage(t, "")
		rec := f.do(req{
			method: http.MethodPost, path: "/admin/login", cookies: []*http.Cookie{pre},
			form: url.Values{"csrf": {csrf}, "token": {tok.Raw}}, hdr: map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"},
		})
		if rec.Code != http.StatusSeeOther {
			t.Errorf("Sec-Fetch-Site same-origin without Origin = %d, want 303", rec.Code)
		}
	})
}

func TestAdmin_CSRFRejected(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	sess, csrf := f.login(t, tok.Raw)
	jar := []*http.Cookie{sess}
	for name, r := range map[string]req{
		"no token":     {form: url.Values{}},
		"wrong token":  {form: url.Values{"csrf": {csrf + "x"}}},
		"cross origin": {form: url.Values{"csrf": {csrf}}, hdr: map[string]string{"Origin": "http://evil.example"}},
		"no origin":    {form: url.Values{"csrf": {csrf}}, hdr: map[string]string{"Origin": ""}},
	} {
		r.method, r.path, r.cookies = http.MethodPost, "/admin/logout", jar
		if rec := f.do(r); rec.Code != http.StatusForbidden || !f.signedIn(sess) {
			t.Errorf("%s: %d, want 403 and the session intact", name, rec.Code)
		}
	}
	// The synchronizer token rotates with the session id.
	sess2, csrf2 := f.login(t, tok.Raw)
	if csrf2 == csrf {
		t.Fatal("csrf token did not rotate with the session")
	}
	if rec := f.do(req{method: http.MethodPost, path: "/admin/logout", cookies: []*http.Cookie{sess2}, form: url.Values{"csrf": {csrf}}}); rec.Code != http.StatusForbidden {
		t.Errorf("previous session's csrf token accepted: %d", rec.Code)
	}
}

func TestAdmin_LoginAudited(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	f.postLogin(t, "bad", "")
	f.login(t, tok.Raw)
	got := strings.Join(f.audit.decisions(domain.AuditAdminLogin), ",")
	if got != "intent,denied,intent,ok" {
		t.Fatalf("login audit = %s", got)
	}
	f.audit.mu.Lock()
	last := f.audit.events[len(f.audit.events)-1]
	f.audit.mu.Unlock()
	if last.Actor != tok.ID {
		t.Errorf("success actor = %q, want the token id", last.Actor)
	}
}

func TestAdmin_LogoutAudited(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	sess, csrf := f.login(t, tok.Raw)
	rec := f.do(req{method: http.MethodPost, path: "/admin/logout", cookies: []*http.Cookie{sess}, form: url.Values{"csrf": {csrf}}})
	if rec.Code != http.StatusSeeOther || f.signedIn(sess) {
		t.Fatalf("logout = %d", rec.Code)
	}
	if c := cookieNamed(rec, cookieSession); c == nil || c.MaxAge >= 0 {
		t.Error("logout must expire the session cookie")
	}
	if got := strings.Join(f.audit.decisions(domain.AuditAdminLogout), ","); got != "intent,ok" {
		t.Fatalf("logout audit = %s", got)
	}
}

func TestAdmin_LoginRateLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
		sess, _ := f.login(t, tok.Raw)
		for i := range loginLimit {
			if rec := f.postLogin(t, "wrong", ""); rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d = %d", i, rec.Code)
			}
		}
		rec := f.postLogin(t, tok.Raw, "")
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("11th attempt = %d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
		}
		if !f.signedIn(sess) {
			t.Fatal("existing session must continue during the lockout")
		}
		<-time.After(loginWindow)
		if rec := f.postLogin(t, tok.Raw, ""); rec.Code != http.StatusSeeOther {
			t.Fatalf("after the window = %d", rec.Code)
		}
	})
}

func TestAdmin_TokenNeverLogged(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	bad := "0123456789abcdef0123456789abcdef-wrong"
	failed := f.postLogin(t, bad, "")
	pre, csrf := f.loginPage(t, "")
	rec := f.do(req{method: http.MethodPost, path: "/admin/login", cookies: []*http.Cookie{pre}, form: url.Values{"csrf": {csrf}, "token": {tok.Raw}}})
	sess := cookieNamed(rec, cookieSession)
	f.audit.mu.Lock()
	var audit strings.Builder
	for _, e := range f.audit.events {
		audit.WriteString(e.String())
	}
	f.audit.mu.Unlock()
	surfaces := map[string]string{
		"log": f.logs.String(), "audit": audit.String(), "location": rec.Header().Get("Location"),
		"failed body": failed.Body.String(), "success body": rec.Body.String(),
	}
	for name, text := range surfaces {
		for _, secret := range []string{tok.Raw, bad, sess.Value, pre.Value, csrf} {
			if strings.Contains(text, secret) {
				t.Errorf("secret leaked into %s", name)
			}
		}
	}
}

func TestAdmin_CSPHeader(t *testing.T) {
	f := newFixture(t)
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		"Cache-Control":           "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff",
	}
	for _, r := range []req{
		{path: "/admin/login"},
		{path: "/admin"},
		{path: "/admin/static/admin.css"},
		{path: "/nope"},
		{path: "/admin/login", host: "evil.example"},
		{path: "/admin/login", peer: "203.0.113.7:1"},
		{method: http.MethodPost, path: "/admin/login", form: url.Values{}},
	} {
		rec := f.do(r)
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s %s (%d): %s = %q", r.method, r.path, rec.Code, k, got)
			}
		}
	}
}

func TestAdmin_EscapesUntrustedNames(t *testing.T) {
	f := newFixture(t)
	evil := `<script>alert(1)</script><img src=x onerror=alert(2)>`
	sess, _ := f.login(t, f.issue(t, evil, sqlitetokens.ScopeAdmin, 0).Raw)
	body := f.do(req{path: "/admin", cookies: []*http.Cookie{sess}}).Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("untrusted token name not escaped: %q", body)
	}
}

func TestAdmin_DirectStoreRevokeAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		revoked := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
		sess, _ := f.login(t, revoked.Raw)
		if err := f.other.Revoke(t.Context(), revoked.ID); err != nil {
			t.Fatal(err)
		}
		if f.signedIn(sess) {
			t.Fatal("a revoke through a second store handle must deny the next request")
		}
		expiring := f.issue(t, "short", sqlitetokens.ScopeAdmin, time.Minute)
		sess, _ = f.login(t, expiring.Raw)
		<-time.After(time.Minute + 2*time.Second)
		if f.signedIn(sess) {
			t.Fatal("an expired backing token must deny the next request")
		}
		idle := f.issue(t, "idle", sqlitetokens.ScopeAdmin, 0)
		sess, _ = f.login(t, idle.Raw)
		<-time.After(idleTTL + time.Second)
		if f.signedIn(sess) {
			t.Fatal("idle timeout not enforced")
		}
		sess, _ = f.login(t, idle.Raw)
		for range int(absoluteTTL / (10 * time.Minute)) {
			<-time.After(10 * time.Minute)
			f.signedIn(sess)
		}
		<-time.After(time.Second)
		if f.signedIn(sess) {
			t.Fatal("absolute timeout not enforced")
		}
	})
}

func TestAdmin_RevocationSweepWithin30s(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); f.s.runSweeper(ctx) }()
		count := func() int { f.s.mu.Lock(); defer f.s.mu.Unlock(); return len(f.s.sessions) }

		revoked := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
		f.login(t, revoked.Raw)
		expiring := f.issue(t, "short", sqlitetokens.ScopeAdmin, 40*time.Second)
		f.login(t, expiring.Raw)
		if err := f.other.Revoke(t.Context(), revoked.ID); err != nil {
			t.Fatal(err)
		}
		<-time.After(SweepInterval)
		synctest.Wait()
		if n := count(); n != 1 {
			t.Fatalf("after one sweep with no requests: %d sessions, want only the unexpired one", n)
		}
		<-time.After(SweepInterval)
		synctest.Wait()
		if n := count(); n != 0 {
			t.Fatalf("expired backing token survived the sweep: %d sessions", n)
		}
		cancel()
		<-done
	})
}

func TestAdmin_AuditFailureFailsClosed(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	sess, csrf := f.login(t, tok.Raw)
	verifies := f.shim.verify.Load()

	f.audit.setFail(true)
	rec := f.postLogin(t, tok.Raw, "")
	if rec.Code != http.StatusServiceUnavailable || cookieNamed(rec, cookieSession) != nil || f.shim.verify.Load() != verifies {
		t.Fatalf("login with audit down = %d, verify calls %d→%d; want 503, no session, no dispatch", rec.Code, verifies, f.shim.verify.Load())
	}
	rec = f.do(req{method: http.MethodPost, path: "/admin/logout", cookies: []*http.Cookie{sess}, form: url.Values{"csrf": {csrf}}})
	if rec.Code != http.StatusServiceUnavailable || !f.signedIn(sess) {
		t.Fatalf("logout with audit down = %d; want 503 and the session untouched", rec.Code)
	}

	// Intent lands, outcome fails: audit_incomplete is latched and shown.
	f.audit.setFail(false)
	f.s.cfg.Audit = &failAfter{inner: f.audit, ok: 1}
	if rec := f.postLogin(t, tok.Raw, ""); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "audit_incomplete") {
		t.Fatalf("outcome failure = %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(f.do(req{path: "/admin", cookies: []*http.Cookie{sess}}).Body.String(), "audit_incomplete") {
		t.Fatal("/admin must show the audit_incomplete banner")
	}
}

// failAfter lets ok appends through, then fails every later one.
type failAfter struct {
	inner *fakeAudit
	ok    int
}

func (a *failAfter) Record(ctx context.Context, e domain.AuditEvent) error {
	if a.ok == 0 {
		return errors.New("disk full")
	}
	a.ok--
	return a.inner.Record(ctx, e)
}
