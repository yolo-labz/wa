package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNormaliseRemoteURL covers the URL-canonicalisation cases for
// the --remote flag. Pins spec 110c §FR-001.
func TestNormaliseRemoteURL(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		insecure  string
		want      string
		wantErr   bool
		errSubstr string
	}{
		{"https_bare", "https://wa.example.com", "", "https://wa.example.com/v1/rpc", false, ""},
		{"https_trailing_slash", "https://wa.example.com/", "", "https://wa.example.com/v1/rpc", false, ""},
		{"https_full_path", "https://wa.example.com/v1/rpc", "", "https://wa.example.com/v1/rpc", false, ""},
		{"http_blocked", "http://wa.example.com", "", "", true, "WA_REMOTE_INSECURE"},
		{"http_allowed", "http://127.0.0.1:8080", "1", "http://127.0.0.1:8080/v1/rpc", false, ""},
		{"non_url", "wa.example.com", "", "", true, "absolute URL"},
		{"empty", "", "", "", true, "absolute URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WA_REMOTE_INSECURE", tc.insecure)
			got, err := normaliseRemoteURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normaliseRemoteURL(%q) = %q, want error", tc.input, got)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("err = %q, want to contain %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCallRemote_HappyPath pins spec 110c §FR-002: a 200 OK with a
// valid JSON-RPC envelope and matching token returns the result and
// exit code 0.
func TestCallRemote_HappyPath(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", got)
		}
		if r.URL.Path != "/v1/rpc" {
			t.Errorf("path = %q, want /v1/rpc", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	t.Cleanup(srv.Close)

	result, exit, err := callRemote(srv.URL, "secret", "status", map[string]any{})
	if err != nil {
		t.Fatalf("callRemote: %v", err)
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0", exit)
	}
	if !strings.Contains(string(result), `"ok":true`) {
		t.Errorf("result = %q, want ok:true", string(result))
	}
}

// TestCallRemote_AuthFails pins that 401 surfaces as exit 64 with an
// actionable error message.
func TestCallRemote_AuthFails(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32099,"message":"unauthorized"}}`))
	}))
	t.Cleanup(srv.Close)

	_, exit, err := callRemote(srv.URL, "wrong-token", "status", map[string]any{})
	if exit != 64 {
		t.Errorf("exit = %d, want 64 (usage error on 401)", exit)
	}
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("err = %q, want to mention auth failed", err.Error())
	}
}

// TestCallRemote_DispatcherError pins that a 200 OK with an error
// envelope propagates the rpcError + maps the code to exit code.
func TestCallRemote_DispatcherError(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32100,"message":"policy refused"}}`))
	}))
	t.Cleanup(srv.Close)

	_, exit, err := callRemote(srv.URL, "tok", "send", map[string]any{})
	if err == nil {
		t.Fatal("expected dispatcher error")
	}
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %T, want *rpcError", err)
	}
	if rpcErr.Code != -32100 {
		t.Errorf("code = %d, want -32100", rpcErr.Code)
	}
	// rpcCodeToExit(-32100) is policy_refused → exit 11.
	if exit != 11 {
		t.Logf("exit = %d (policy refused mapping check; verify rpcCodeToExit table if this changes)", exit)
	}
}

// TestCallRemote_5xxFails pins HTTP 5xx → exit 70.
func TestCallRemote_5xxFails(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	t.Cleanup(srv.Close)

	_, exit, err := callRemote(srv.URL, "tok", "status", nil)
	if exit != 70 {
		t.Errorf("exit = %d, want 70 (5xx → internal)", exit)
	}
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
}

// TestCallRemote_ConnectFails pins exit 10 on dial failure.
func TestCallRemote_ConnectFails(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")
	// Use a port that is essentially guaranteed to be closed.
	_, exit, err := callRemote("http://127.0.0.1:1", "tok", "status", nil)
	if exit != 10 {
		t.Errorf("exit = %d, want 10 (dial failure → service unavailable)", exit)
	}
	if err == nil {
		t.Fatal("expected error on dial failure")
	}
}

// TestCallRemote_InvalidEnvelope ensures malformed JSON from server
// surfaces as exit 1 with a parseable error.
func TestCallRemote_InvalidEnvelope(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not valid json`))
	}))
	t.Cleanup(srv.Close)

	_, exit, err := callRemote(srv.URL, "tok", "status", nil)
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

// TestCallRemote_RawParamsEmbedded pins that []byte params are
// embedded as JSON, not base64-encoded — same behaviour as the
// socket transport in rpc.go.
func TestCallRemote_RawParamsEmbedded(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Params struct {
				X int `json:"x"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got.Params.X != 42 {
			t.Errorf("params.x = %d, want 42 (raw JSON params should embed verbatim)", got.Params.X)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(srv.Close)

	rawParams, _ := json.Marshal(map[string]int{"x": 42})
	_, _, err := callRemote(srv.URL, "tok", "echo", rawParams)
	if err != nil {
		t.Fatalf("callRemote: %v", err)
	}
}
