package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore is a minimal TokenStore double for handler tests.
type fakeStore struct {
	mu      sync.Mutex
	tokens  map[string]Scope // raw token → scope
	revoked map[string]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tokens:  make(map[string]Scope),
		revoked: make(map[string]bool),
	}
}

func (f *fakeStore) Add(raw string, scope Scope) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[raw] = scope
}

func (f *fakeStore) Verify(_ context.Context, rawToken string) (Scope, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revoked[rawToken] {
		return Scope(""), "", errors.New("revoked")
	}
	scope, ok := f.tokens[rawToken]
	if !ok {
		return Scope(""), "", errors.New("not found")
	}
	return scope, "tok-id", nil
}

func newScopedServerForTest(t *testing.T, store *fakeStore) *Server {
	t.Helper()
	srv, err := NewServer(t.Context(), "127.0.0.1:0",
		&fakeDispatcher{handler: func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}},
		NewScopedAuth(store),
		WithLogger(slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	})
	return srv
}

func postScoped(t *testing.T, addr, token, body string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/rpc", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decodeRespBody(t *testing.T, resp *http.Response) rpcResponse {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out rpcResponse
	_ = json.Unmarshal(raw, &out)
	return out
}

// TestScopedAuth_AdminPasses pins admin-scope reaches every method
// (using send as a representative).
func TestScopedAuth_AdminPasses(t *testing.T) {
	store := newFakeStore()
	store.Add("adm-tok", ScopeAdminStr)
	srv := newScopedServerForTest(t, store)

	resp := postScoped(t, srv.ListenerAddr().String(), "adm-tok", //nolint:bodyclose
		`{"jsonrpc":"2.0","id":1,"method":"send","params":{}}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin /send status = %d, want 200", resp.StatusCode)
	}
}

// TestScopedAuth_ReadCannotSend pins that a read-only token is
// refused on send-scope methods with HTTP 403.
func TestScopedAuth_ReadCannotSend(t *testing.T) {
	store := newFakeStore()
	store.Add("ro-tok", ScopeReadStr)
	srv := newScopedServerForTest(t, store)

	resp := postScoped(t, srv.ListenerAddr().String(), "ro-tok", //nolint:bodyclose
		`{"jsonrpc":"2.0","id":1,"method":"send","params":{}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("read-only /send status = %d, want 403", resp.StatusCode)
	}
	out := decodeRespBody(t, resp)
	if out.Error == nil || !strings.Contains(out.Error.Message, "scope insufficient") {
		t.Errorf("error = %+v, want scope-insufficient envelope", out.Error)
	}
}

// TestScopedAuth_SendCannotAdmin pins that a send-scope token is
// refused on admin-scope methods.
func TestScopedAuth_SendCannotAdmin(t *testing.T) {
	store := newFakeStore()
	store.Add("send-tok", ScopeSendStr)
	srv := newScopedServerForTest(t, store)

	resp := postScoped(t, srv.ListenerAddr().String(), "send-tok", //nolint:bodyclose
		`{"jsonrpc":"2.0","id":1,"method":"pair","params":{}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("send /pair status = %d, want 403", resp.StatusCode)
	}
}

// TestScopedAuth_ReadReachesStatus pins that a read-only token can
// invoke read-scope methods.
func TestScopedAuth_ReadReachesStatus(t *testing.T) {
	store := newFakeStore()
	store.Add("ro-tok", ScopeReadStr)
	srv := newScopedServerForTest(t, store)

	resp := postScoped(t, srv.ListenerAddr().String(), "ro-tok", //nolint:bodyclose
		`{"jsonrpc":"2.0","id":1,"method":"status","params":{}}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read /status status = %d, want 200", resp.StatusCode)
	}
}

// TestScopedAuth_UnknownMethodFailsClosed pins that a method NOT in
// MethodScopes returns 403 even with admin scope. Spec 110d
// `AllowedScope` "fail closed" rule.
func TestScopedAuth_UnknownMethodFailsClosed(t *testing.T) {
	store := newFakeStore()
	store.Add("adm-tok", ScopeAdminStr)
	srv := newScopedServerForTest(t, store)

	resp := postScoped(t, srv.ListenerAddr().String(), "adm-tok", //nolint:bodyclose
		`{"jsonrpc":"2.0","id":1,"method":"future.unknown","params":{}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("unknown method status = %d, want 403 (fail closed)", resp.StatusCode)
	}
}

// TestAllowedScope_Table pins the comparison rule directly: a higher
// scope always satisfies a lower MethodScope.
func TestAllowedScope_Table(t *testing.T) {
	cases := []struct {
		method  string
		granted MethodScope
		want    bool
	}{
		{"status", ScopeRead, true},
		{"status", ScopeSend, true},
		{"status", ScopeAdmin, true},
		{"send", ScopeRead, false},
		{"send", ScopeSend, true},
		{"send", ScopeAdmin, true},
		{"pair", ScopeRead, false},
		{"pair", ScopeSend, false},
		{"pair", ScopeAdmin, true},
		{"future.unknown", ScopeAdmin, false}, // fail closed
	}
	for _, tc := range cases {
		t.Run(tc.method+"/"+map[MethodScope]string{ScopeRead: "read", ScopeSend: "send", ScopeAdmin: "admin"}[tc.granted], func(t *testing.T) {
			if got := AllowedScope(tc.method, tc.granted); got != tc.want {
				t.Errorf("AllowedScope(%q, %v) = %v, want %v", tc.method, tc.granted, got, tc.want)
			}
		})
	}
}
