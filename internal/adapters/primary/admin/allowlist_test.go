package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	fullJID  = "5511987654321@s.whatsapp.net"
	groupJID = "120363012345678901@g.us"
)

// fakeRPC records every dispatched `allow` call and answers op=list
// from canned rules. The real op handler is exercised end to end in
// cmd/wad (TestAdminAllowlist_AddRemovePerAction).
type fakeRPC struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (r *fakeRPC) Handle(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if method != "allow" {
		return nil, errors.New("unexpected method " + method)
	}
	if strings.Contains(string(params), `"op":"list"`) {
		return json.RawMessage(`{"rules":[{"jid":"` + fullJID + `","actions":["read","send"]},{"jid":"` + groupJID + `","actions":["group.add"]}]}`), nil
	}
	r.calls = append(r.calls, string(params))
	if r.fail {
		return nil, errors.New("persist failed")
	}
	return json.RawMessage(`{}`), nil
}

func (r *fakeRPC) mutations() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (f *fixture) allow(sess *http.Cookie, op string, form url.Values) int {
	return f.do(req{method: http.MethodPost, path: "/admin/allowlist/" + op, cookies: []*http.Cookie{sess}, form: form}).Code
}

func TestAdminAllowlist_MasksJIDs(t *testing.T) {
	f := newFixture(t)
	sess, _ := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	rec := f.do(req{path: "/admin/allowlist", cookies: []*http.Cookie{sess}})
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/allowlist = %d", rec.Code)
	}
	for _, leak := range []string{fullJID, "5511987654321", "551198765", groupJID, "1203630123456"} {
		if strings.Contains(body, leak) {
			t.Errorf("list view leaks %q", leak)
		}
	}
	for _, want := range []string{"•••4321@s.whatsapp.net", "•••8901@g.us", "read, send", "group.add"} {
		if !strings.Contains(body, want) {
			t.Errorf("list view missing %q", want)
		}
	}
	if maskJID("12@lid") != "•••@lid" {
		t.Errorf("short user part must be fully masked: %q", maskJID("12@lid"))
	}
}

func TestAdminAllowlist_CSRFRejected(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	for _, op := range []string{"add", "remove"} {
		form := url.Values{"jid": {fullJID}, "action": {"send"}}
		cases := map[string]req{
			"no token":     {form: form},
			"wrong token":  {form: url.Values{"csrf": {csrf + "x"}, "jid": {fullJID}, "action": {"send"}}},
			"cross origin": {form: url.Values{"csrf": {csrf}, "jid": {fullJID}, "action": {"send"}}, hdr: map[string]string{"Origin": "http://evil.example"}},
			"no session":   {form: url.Values{"csrf": {csrf}, "jid": {fullJID}, "action": {"send"}}, cookies: []*http.Cookie{}},
		}
		for name, r := range cases {
			r.method, r.path = http.MethodPost, "/admin/allowlist/"+op
			if r.cookies == nil {
				r.cookies = []*http.Cookie{sess}
			}
			if rec := f.do(r); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403", op, name, rec.Code)
			}
		}
	}
	if m := f.rpc.mutations(); len(m) != 0 {
		t.Fatalf("refused requests reached the allow handler: %v", m)
	}
	if n := len(f.audit.decisions(domain.AuditGrant)) + len(f.audit.decisions(domain.AuditRevoke)); n != 0 {
		t.Fatalf("refused requests wrote %d audit rows", n)
	}
}

func TestAdminAllowlist_Audited(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0)
	sess, csrf := f.login(t, tok.Raw)
	if c := f.allow(sess, "add", url.Values{"csrf": {csrf}, "jid": {fullJID}, "action": {"read", "send"}}); c != http.StatusSeeOther {
		t.Fatalf("add = %d", c)
	}
	if c := f.allow(sess, "remove", url.Values{"csrf": {csrf}, "jid": {fullJID}, "action": {"send"}}); c != http.StatusSeeOther {
		t.Fatalf("remove = %d", c)
	}
	want := []string{
		`{"actions":["read","send"],"jid":"` + fullJID + `","op":"add"}`,
		`{"actions":["send"],"jid":"` + fullJID + `","op":"remove"}`,
	}
	if got := f.rpc.mutations(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("dispatched %v, want %v", got, want)
	}
	for act, name := range map[domain.AuditAction]string{domain.AuditGrant: "grant", domain.AuditRevoke: "revoke"} {
		if got := strings.Join(f.audit.decisions(act), ","); got != "intent,ok" {
			t.Errorf("%s audit = %s", name, got)
		}
	}
	f.audit.mu.Lock()
	for _, e := range f.audit.events {
		if e.Action == domain.AuditGrant && (e.Actor != tok.ID || e.Subject.String() != fullJID) {
			t.Errorf("grant row actor %q subject %q", e.Actor, e.Subject)
		}
	}
	rows := len(f.audit.events)
	f.audit.mu.Unlock()
	// Invalid input is refused before any audit row or dispatch.
	for _, form := range []url.Values{
		{"csrf": {csrf}, "jid": {"not a jid"}, "action": {"send"}},
		{"csrf": {csrf}, "jid": {fullJID}},
		{"csrf": {csrf}, "jid": {fullJID}, "action": {"panic"}},
	} {
		if c := f.allow(sess, "add", form); c != http.StatusBadRequest {
			t.Errorf("invalid %v = %d, want 400", form, c)
		}
	}
	f.audit.mu.Lock()
	after := len(f.audit.events)
	f.audit.mu.Unlock()
	if after != rows || len(f.rpc.mutations()) != 2 {
		t.Errorf("invalid input reached audit (%d→%d rows) or dispatch", rows, after)
	}
}

func TestAdminAllowlist_AuditFailureFailsClosed(t *testing.T) {
	f := newFixture(t)
	sess, csrf := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	form := url.Values{"csrf": {csrf}, "jid": {fullJID}, "action": {"send"}}

	f.audit.setFail(true)
	for _, op := range []string{"add", "remove"} {
		if c := f.allow(sess, op, form); c != http.StatusServiceUnavailable {
			t.Errorf("%s with audit down = %d, want 503", op, c)
		}
	}
	if m := f.rpc.mutations(); len(m) != 0 {
		t.Fatalf("mutation dispatched without an intent row: %v", m)
	}

	f.audit.setFail(false)
	f.s.cfg.Audit = &failAfter{inner: f.audit, ok: 1}
	rec := f.do(req{method: http.MethodPost, path: "/admin/allowlist/add", cookies: []*http.Cookie{sess}, form: form})
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "audit_incomplete") {
		t.Fatalf("outcome failure = %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(f.do(req{path: "/admin/allowlist", cookies: []*http.Cookie{sess}}).Body.String(), "audit_incomplete") {
		t.Fatal("/admin/allowlist must show the audit_incomplete banner")
	}

	// A failed dispatch is recorded as failed, never as ok.
	f.s.cfg.Audit = f.audit
	f.rpc.fail = true
	if c := f.allow(sess, "add", form); c != http.StatusInternalServerError {
		t.Errorf("failed dispatch = %d, want 500", c)
	}
	if d := f.audit.decisions(domain.AuditGrant); d[len(d)-1] != "failed" {
		t.Errorf("failed dispatch audited as %v", d)
	}
}
