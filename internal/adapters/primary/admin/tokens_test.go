package admin

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

var issuedRE = regexp.MustCompile(`<code id="issued">([^<]+)</code>`)

func (f *fixture) tokenPost(sess *http.Cookie, op string, form url.Values) *httpResult {
	rec := f.do(req{method: http.MethodPost, path: "/admin/tokens/" + op, cookies: []*http.Cookie{sess}, form: form})
	return &httpResult{code: rec.Code, body: rec.Body.String(), location: rec.Header().Get("Location")}
}

type httpResult struct {
	code           int
	body, location string
}

// rowOf returns the <tr> of the token list holding id.
func rowOf(t *testing.T, body, id string) string {
	t.Helper()
	_, after, ok := strings.Cut(body, "<code>"+id+"</code>")
	row, _, closed := strings.Cut(after, "</tr>")
	if !ok || !closed {
		t.Fatalf("no row for %s", id)
	}
	return row
}

func (f *fixture) storeTokens(t *testing.T) []sqlitetokens.Token {
	t.Helper()
	l, err := f.other.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestAdminTokens_IssueShownOnceAndAudited(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	res := f.tokenPost(sess, "issue", url.Values{"csrf": {csrf}, "name": {"agent-1"}, "scope": {"send"}, "ttl_days": {"30"}})
	m := issuedRE.FindStringSubmatch(res.body)
	if res.code != http.StatusOK || m == nil || res.location != "" {
		t.Fatalf("issue = %d location %q; want 200 with the secret in the body and no redirect", res.code, res.location)
	}
	raw := m[1]
	tok, err := f.other.Verify(t.Context(), raw)
	if err != nil || tok.Name != "agent-1" || tok.Scope != sqlitetokens.ScopeSend || tok.ExpiresAt.IsZero() {
		t.Fatalf("issued token: %+v %v", tok, err)
	}
	list := f.do(req{path: "/admin/tokens", cookies: []*http.Cookie{sess}}).Body.String()
	if strings.Contains(list, raw) || !strings.Contains(list, "agent-1") || !strings.Contains(list, tok.ID) {
		t.Fatal("the list must show the token's row but never its secret again")
	}
	if got := strings.Join(f.audit.decisions(domain.AuditTokenIssue), ","); got != "intent,ok" {
		t.Fatalf("issue audit = %s", got)
	}
	f.audit.mu.Lock()
	last := f.audit.events[len(f.audit.events)-1]
	leaked := false
	for _, e := range f.audit.events {
		leaked = leaked || strings.Contains(e.String(), raw)
	}
	f.audit.mu.Unlock()
	if !strings.Contains(last.Detail, tok.ID) {
		t.Errorf("outcome detail %q must name the token id", last.Detail)
	}
	if leaked || strings.Contains(f.logs.String(), raw) {
		t.Error("secret leaked into the audit or daemon log")
	}
	// Invalid input is refused before audit or issue.
	before := len(f.storeTokens(t))
	for _, form := range []url.Values{
		{"csrf": {csrf}, "name": {""}, "scope": {"send"}},
		{"csrf": {csrf}, "name": {"x"}, "scope": {"root"}},
		{"csrf": {csrf}, "name": {"x"}, "scope": {"read"}, "ttl_days": {"-1"}},
	} {
		if res := f.tokenPost(sess, "issue", form); res.code != http.StatusBadRequest {
			t.Errorf("invalid %v = %d", form, res.code)
		}
	}
	if n := len(f.storeTokens(t)); n != before {
		t.Errorf("invalid issue created tokens: %d → %d", before, n)
	}
}

func TestAdminTokens_RevokeAuditedAndDropsSessions(t *testing.T) {
	f := newFixture(t)
	opsSess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	other := f.issue(t, "laptop", sqlitetokens.ScopeAdmin, 0)
	laptop1, _ := f.login(t, other.Raw)
	laptop2, _ := f.login(t, other.Raw)
	res := f.tokenPost(opsSess, "revoke", url.Values{"csrf": {csrf}, "id": {other.ID}})
	if res.code != http.StatusSeeOther || res.location != "/admin/tokens" {
		t.Fatalf("revoke = %d %q", res.code, res.location)
	}
	f.s.mu.Lock()
	n := len(f.s.sessions)
	f.s.mu.Unlock()
	if n != 1 || f.signedIn(laptop1) || f.signedIn(laptop2) || !f.signedIn(opsSess) {
		t.Fatalf("after revoke: %d sessions; both laptop sessions must be gone at once, ops kept", n)
	}
	if _, err := f.other.Lookup(t.Context(), other.ID); !errors.Is(err, sqlitetokens.ErrTokenRevoked) {
		t.Fatalf("store: %v, want revoked", err)
	}
	if got := strings.Join(f.audit.decisions(domain.AuditTokenRevoke), ","); got != "intent,ok" {
		t.Fatalf("revoke audit = %s", got)
	}
	body := f.do(req{path: "/admin/tokens", cookies: []*http.Cookie{opsSess}}).Body.String()
	if strings.Contains(rowOf(t, body, other.ID), "Revoke</button>") {
		t.Error("a revoked token must not offer Revoke again")
	}
	// Unknown id: audited as failed, not ok.
	if res := f.tokenPost(opsSess, "revoke", url.Values{"csrf": {csrf}, "id": {"nope"}}); res.code != http.StatusBadRequest {
		t.Errorf("revoke unknown = %d", res.code)
	}
	if d := f.audit.decisions(domain.AuditTokenRevoke); d[len(d)-1] != "failed" {
		t.Errorf("unknown revoke audited as %v", d)
	}
}

func TestAdminTokens_CannotRevokeOwnSessionToken(t *testing.T) {
	f := newFixture(t)
	own := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	sess, csrf := f.login(t, own.Raw)
	body := f.do(req{path: "/admin/tokens", cookies: []*http.Cookie{sess}}).Body.String()
	if row := rowOf(t, body, own.ID); strings.Contains(row, "Revoke</button>") || !strings.Contains(row, "this session") {
		t.Error("the session's own token must show 'this session', not a Revoke button")
	}
	res := f.tokenPost(sess, "revoke", url.Values{"csrf": {csrf}, "id": {own.ID}})
	if res.code != http.StatusBadRequest || !strings.Contains(res.body, "Sign out") {
		t.Fatalf("own revoke = %d", res.code)
	}
	if _, err := f.other.Lookup(t.Context(), own.ID); err != nil || !f.signedIn(sess) {
		t.Fatalf("own token must stay valid and the session alive: %v", err)
	}
	if d := f.audit.decisions(domain.AuditTokenRevoke); len(d) != 0 {
		t.Errorf("refused own-revoke wrote audit rows %v", d)
	}
}

func TestAdminTokens_CSRFRejected(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	victim := f.issue(t, "victim", sqlitetokens.ScopeSend, 0)
	before := len(f.storeTokens(t))
	forms := map[string]url.Values{
		"issue":  {"name": {"evil"}, "scope": {"admin"}},
		"revoke": {"id": {victim.ID}},
	}
	for op, base := range forms {
		with := func(csrf string) url.Values {
			v := url.Values{}
			for k, vs := range base {
				v[k] = vs
			}
			if csrf != "" {
				v.Set("csrf", csrf)
			}
			return v
		}
		for name, r := range map[string]req{
			"no token":     {form: with("")},
			"wrong token":  {form: with(csrf + "x")},
			"cross origin": {form: with(csrf), hdr: map[string]string{"Origin": "http://evil.example"}},
			"no origin":    {form: with(csrf), hdr: map[string]string{"Origin": ""}},
			"no session":   {form: with(csrf), cookies: []*http.Cookie{}},
		} {
			r.method, r.path = http.MethodPost, "/admin/tokens/"+op
			if r.cookies == nil {
				r.cookies = []*http.Cookie{sess}
			}
			if rec := f.do(r); rec.Code != http.StatusForbidden || issuedRE.MatchString(rec.Body.String()) {
				t.Errorf("%s %s = %d, want 403", op, name, rec.Code)
			}
		}
	}
	if n := len(f.storeTokens(t)); n != before {
		t.Errorf("forged issue created a token: %d → %d", before, n)
	}
	if _, err := f.other.Lookup(t.Context(), victim.ID); err != nil {
		t.Errorf("forged revoke took effect: %v", err)
	}
	if n := len(f.audit.decisions(domain.AuditTokenIssue)) + len(f.audit.decisions(domain.AuditTokenRevoke)); n != 0 {
		t.Errorf("refused requests wrote %d audit rows", n)
	}
}

func TestAdminTokens_DirectStoreRevokeAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ops := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
		opsSess, csrf := f.login(t, ops.Raw)
		laptop := f.issue(t, "laptop", sqlitetokens.ScopeAdmin, 0)
		laptopSess, _ := f.login(t, laptop.Raw)
		if err := f.other.Revoke(t.Context(), laptop.ID); err != nil { // as `wad token revoke` would
			t.Fatal(err)
		}
		if f.signedIn(laptopSess) {
			t.Fatal("a direct store revoke must deny the next request")
		}
		body := f.do(req{path: "/admin/tokens", cookies: []*http.Cookie{opsSess}}).Body.String()
		if row := rowOf(t, body, laptop.ID); strings.Contains(row, "Revoke</button>") || strings.Count(row, "<td></td>") > 1 {
			t.Errorf("directly revoked token must list its revoked time: %s", row)
		}
		// A UI-issued short-lived admin token: its session dies at expiry.
		res := f.tokenPost(opsSess, "issue", url.Values{"csrf": {csrf}, "name": {"day"}, "scope": {"admin"}, "ttl_days": {"1"}})
		m := issuedRE.FindStringSubmatch(res.body)
		if m == nil {
			t.Fatalf("issue = %d", res.code)
		}
		daySess, _ := f.login(t, m[1])
		<-time.After(24*time.Hour + time.Second)
		if f.signedIn(daySess) {
			t.Fatal("a session must die with its token's expiry")
		}
	})
}

func TestAdminTokens_AuditFailureFailsClosed(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	victim := f.issue(t, "victim", sqlitetokens.ScopeSend, 0)
	before := len(f.storeTokens(t))

	f.audit.setFail(true)
	if res := f.tokenPost(sess, "issue", url.Values{"csrf": {csrf}, "name": {"x"}, "scope": {"read"}}); res.code != http.StatusServiceUnavailable || issuedRE.MatchString(res.body) {
		t.Errorf("issue with audit down = %d", res.code)
	}
	if res := f.tokenPost(sess, "revoke", url.Values{"csrf": {csrf}, "id": {victim.ID}}); res.code != http.StatusServiceUnavailable {
		t.Errorf("revoke with audit down = %d", res.code)
	}
	if n := len(f.storeTokens(t)); n != before {
		t.Fatalf("issue dispatched without an intent row: %d → %d", before, n)
	}
	if _, err := f.other.Lookup(t.Context(), victim.ID); err != nil {
		t.Fatalf("revoke dispatched without an intent row: %v", err)
	}

	// Intent lands, outcome fails: the secret is still shown once (the
	// token exists), with 503 and the audit_incomplete banner.
	f.audit.setFail(false)
	f.s.cfg.Audit = &failAfter{inner: f.audit, ok: 1}
	res := f.tokenPost(sess, "issue", url.Values{"csrf": {csrf}, "name": {"y"}, "scope": {"read"}})
	if res.code != http.StatusServiceUnavailable || !issuedRE.MatchString(res.body) || !strings.Contains(res.body, "audit_incomplete") {
		t.Fatalf("issue outcome failure = %d", res.code)
	}
	f.s.cfg.Audit = &failAfter{inner: f.audit, ok: 1}
	if res := f.tokenPost(sess, "revoke", url.Values{"csrf": {csrf}, "id": {victim.ID}}); res.code != http.StatusServiceUnavailable || !strings.Contains(res.body, "audit_incomplete") {
		t.Fatalf("revoke outcome failure = %d", res.code)
	}
	if !strings.Contains(f.do(req{path: "/admin/tokens", cookies: []*http.Cookie{sess}}).Body.String(), "audit_incomplete") {
		t.Fatal("/admin/tokens must show the audit_incomplete banner")
	}
}
