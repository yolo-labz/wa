package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// stubScopeStore is a TokenStore granting a fixed scope to one token.
type stubScopeStore struct {
	token string
	scope Scope
}

func (s *stubScopeStore) Verify(_ context.Context, raw string) (Scope, string, error) {
	if raw == s.token {
		return s.scope, "tok-1", nil
	}
	return Scope(""), "", ErrUnauthorized
}

// mcpEcho is a stand-in MCP handler that records which scope class it
// was built for.
func mcpEcho(label string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, label)
	})
}

func getMCP(t *testing.T, addr, token string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/mcp", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestMCPRoute_AuthAndScopeRouting pins feature 111 M2 FR-111-02: /mcp
// requires auth, env-token auth implies admin, scoped tokens route to
// their scope-class handler, and unknown scopes fail closed.
func TestMCPRoute_AuthAndScopeRouting(t *testing.T) {
	provider := func(scope Scope) http.Handler {
		switch scope {
		case ScopeReadStr:
			return mcpEcho("read-handler")
		case ScopeSendStr, ScopeAdminStr:
			return mcpEcho("full-handler")
		default:
			return nil
		}
	}

	t.Run("env token implies admin", func(t *testing.T) {
		token := strings.Repeat("a", 64)
		srv, err := NewServer(t.Context(), "127.0.0.1:0", &fakeDispatcher{}, NewEnvTokenAuth(token),
			WithLogger(discardLogger()), WithMCP(provider))
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		go func() { _ = srv.Serve() }()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
		addr := srv.ListenerAddr().String()

		status, body := getMCP(t, addr, token)
		if status != http.StatusOK || body != "full-handler" {
			t.Fatalf("admin route: status=%d body=%q, want 200 full-handler", status, body)
		}
		status, _ = getMCP(t, addr, "")
		if status != http.StatusUnauthorized {
			t.Fatalf("missing token: status=%d, want 401", status)
		}
		status, _ = getMCP(t, addr, "wrong-token-wrong-token-wrong-token-wrong-token-wrong-token-wro")
		if status != http.StatusUnauthorized {
			t.Fatalf("bad token: status=%d, want 401", status)
		}
	})

	t.Run("scoped store routes per scope", func(t *testing.T) {
		for _, tc := range []struct {
			scope    Scope
			wantBody string
			wantCode int
		}{
			{ScopeReadStr, "read-handler", http.StatusOK},
			{ScopeSendStr, "full-handler", http.StatusOK},
			{ScopeAdminStr, "full-handler", http.StatusOK},
			{Scope("weird"), "", http.StatusForbidden},
		} {
			store := &stubScopeStore{token: "tok-" + string(tc.scope), scope: tc.scope}
			srv, err := NewServer(t.Context(), "127.0.0.1:0", &fakeDispatcher{}, NewScopedAuth(store),
				WithLogger(discardLogger()), WithMCP(provider))
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			go func() { _ = srv.Serve() }()
			addr := srv.ListenerAddr().String()

			status, body := getMCP(t, addr, store.token)
			if status != tc.wantCode {
				t.Errorf("scope %q: status=%d, want %d", tc.scope, status, tc.wantCode)
			}
			if tc.wantBody != "" && body != tc.wantBody {
				t.Errorf("scope %q: body=%q, want %q", tc.scope, body, tc.wantBody)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
		}
	})

	t.Run("no provider, no route", func(t *testing.T) {
		token := strings.Repeat("b", 64)
		srv, err := NewServer(t.Context(), "127.0.0.1:0", &fakeDispatcher{}, NewEnvTokenAuth(token),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		go func() { _ = srv.Serve() }()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
		status, _ := getMCP(t, srv.ListenerAddr().String(), token)
		if status != http.StatusNotFound {
			t.Fatalf("unwired /mcp: status=%d, want 404", status)
		}
	})
}
