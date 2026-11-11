package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/admin"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// allowOnlyRPC routes the admin UI's `allow` calls to the real handler.
type allowOnlyRPC func(context.Context, json.RawMessage) (json.RawMessage, error)

func (f allowOnlyRPC) Handle(ctx context.Context, _ string, p json.RawMessage) (json.RawMessage, error) {
	return f(ctx, p)
}

var adminCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// adminPost sends a same-origin form POST over loopback.
func adminPost(t *testing.T, h http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Host, r.RemoteAddr = "localhost:9311", "127.0.0.1:40000"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://localhost:9311")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func adminGet(t *testing.T, h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	r.Host, r.RemoteAddr = "localhost:9311", "127.0.0.1:40000"
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func csrfOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body, _ := io.ReadAll(rec.Body)
	m := adminCSRF.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf field (status %d)", rec.Code)
	}
	return string(m[1])
}

// TestAdminAllowlist_AddRemovePerAction drives FR-116-04 through the
// admin HTTP surface into the daemon's real `allow` handler and its
// persisted file: grants and revokes are per action, never all-or-none.
func TestAdminAllowlist_AddRemovePerAction(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlitetokens.Open(t.Context(), filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tok, err := store.Issue(t.Context(), "ops", sqlitetokens.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	al := domain.NewAllowlist()
	var mu sync.RWMutex
	path := filepath.Join(dir, "allowlist.toml")
	allow := handleAllow(al, &mu, path, nopAudit{}, slog.New(slog.DiscardHandler))
	h := admin.New(admin.Config{
		Profile: "default", Tokens: adminTokenShim{store}, Audit: nopAudit{},
		RPC: allowOnlyRPC(allow),
	}).Handler()

	page := adminGet(t, h, "/admin/login")
	rec := adminPost(t, h, "/admin/login", url.Values{"csrf": {csrfOf(t, page)}, "token": {tok.Raw}}, page.Result().Cookies()...)
	var sess *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-wa_admin" {
			sess = c
		}
	}
	if sess == nil {
		t.Fatalf("login = %d", rec.Code)
	}
	csrf := csrfOf(t, adminGet(t, h, "/admin/allowlist", sess))
	jid, _ := domain.Parse("5511987654321@s.whatsapp.net")

	step := func(op string, actions ...string) {
		t.Helper()
		rec := adminPost(t, h, "/admin/allowlist/"+op, url.Values{"csrf": {csrf}, "jid": {jid.String()}, "action": actions}, sess)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s %v = %d %q", op, actions, rec.Code, rec.Body.String())
		}
	}
	want := func(grants map[domain.Action]bool) {
		t.Helper()
		for a, on := range grants {
			if al.Allows(jid, a) != on {
				t.Errorf("%s granted = %t, want %t", a, !on, on)
			}
		}
		loaded, err := loadAllowlist(path)
		if err != nil {
			t.Fatal(err)
		}
		for a, on := range grants {
			if loaded.Allows(jid, a) != on {
				t.Errorf("persisted %s = %t, want %t", a, !on, on)
			}
		}
	}
	step("add", "read", "send", "group.add")
	want(map[domain.Action]bool{domain.ActionRead: true, domain.ActionSend: true, domain.ActionGroupAdd: true, domain.ActionGroupCreate: false})
	step("remove", "send")
	want(map[domain.Action]bool{domain.ActionRead: true, domain.ActionSend: false, domain.ActionGroupAdd: true})
	step("add", "group.create")
	step("remove", "read", "group.add")
	want(map[domain.Action]bool{domain.ActionRead: false, domain.ActionGroupAdd: false, domain.ActionGroupCreate: true})

	// The CLI form (no actions) still revokes everything.
	if _, err := allow(t.Context(), json.RawMessage(`{"op":"remove","jid":"`+jid.String()+`"}`)); err != nil {
		t.Fatal(err)
	}
	if al.Size() != 0 {
		t.Fatalf("allow remove without actions left %d entries", al.Size())
	}
}
