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
	"testing"
	"time"
)

// fakeDispatcher is a minimal Dispatcher double for handler tests.
type fakeDispatcher struct {
	handler func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

func (f *fakeDispatcher) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if f.handler == nil {
		return nil, errors.New("fakeDispatcher: no handler set")
	}
	return f.handler(ctx, method, params)
}

// rpcCodedErr is a minimal stand-in for the dispatcher's typed
// errors (codedError interface). Used to verify codeFromError.
type rpcCodedErr struct {
	code int
	msg  string
}

func (e *rpcCodedErr) Error() string { return e.msg }
func (e *rpcCodedErr) RPCCode() int  { return e.code }

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newServerForTest(t *testing.T, fd *fakeDispatcher, token string) *Server {
	t.Helper()
	srv, err := NewServer(t.Context(), "127.0.0.1:0", fd, NewEnvTokenAuth(token), WithLogger(discardLogger()))
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

func postJSON(t *testing.T, addr, token, body string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/rpc", strings.NewReader(body))
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

func decodeResp(t *testing.T, resp *http.Response) rpcResponse {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %q: %v", string(raw), err)
	}
	return out
}

// decodeProblem decodes an RFC 9457 body (feature 113) and pins the
// problem+json media type plus the status echo every non-200 REST
// response must carry.
func decodeProblem(t *testing.T, resp *http.Response) problemDetails {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out problemDetails
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal problem %q: %v", string(raw), err)
	}
	if out.Status != resp.StatusCode {
		t.Errorf("problem status = %d, want HTTP status %d", out.Status, resp.StatusCode)
	}
	return out
}

// TestRPC_HappyPath pins spec 110a §FR-001: a properly authenticated
// JSON-RPC envelope reaches Dispatcher.Handle and the result returns
// in a JSON-RPC envelope with matching id.
func TestRPC_HappyPath(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			if method != "echo" {
				t.Errorf("method = %q, want echo", method)
			}
			return json.RawMessage(`{"echoed":true,"params":` + string(params) + `}`), nil
		},
	}
	srv := newServerForTest(t, fd, "secret")
	resp := postJSON(t, srv.ListenerAddr().String(), "secret", //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
		`{"jsonrpc":"2.0","id":7,"method":"echo","params":{"hi":"bye"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeResp(t, resp)
	if out.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", out.JSONRPC)
	}
	if string(out.ID) != "7" {
		t.Errorf("id = %s, want 7", string(out.ID))
	}
	if out.Error != nil {
		t.Errorf("unexpected error: %+v", out.Error)
	}
}

// TestRPC_AuthFails pins spec 110a §FR-002: missing / wrong / empty
// bearer token returns 401 with an RFC 9457 problem body (feature
// 113). Constant-time compare means timing-attack resistance.
func TestRPC_AuthFails(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("dispatcher reached on auth failure")
			return nil, nil
		},
	}
	srv := newServerForTest(t, fd, "expected")

	cases := []struct {
		name  string
		token string
	}{
		{"missing", ""},
		{"wrong", "wrong"},
		{"empty_bearer", ""}, // separate from missing because of the prefix branch
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, srv.ListenerAddr().String(), tc.token, //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
				`{"jsonrpc":"2.0","id":1,"method":"any","params":{}}`)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
			out := decodeProblem(t, resp)
			if out.Code != -32099 {
				t.Errorf("problem code = %d, want -32099", out.Code)
			}
			if !strings.Contains(out.Detail, "unauthorized") {
				t.Errorf("problem detail = %q, want to mention unauthorized", out.Detail)
			}
		})
	}
}

// TestRPC_DisabledAuthRejects pins the safety property that
// constructing the authenticator with an empty token (programming
// error) does NOT silently allow all requests.
func TestRPC_DisabledAuthRejects(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("dispatcher reached when auth was constructed disabled")
			return nil, nil
		},
	}
	srv := newServerForTest(t, fd, "")                                      // empty raw token = disabled
	resp := postJSON(t, srv.ListenerAddr().String(), "any-presented-token", //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
		`{"jsonrpc":"2.0","id":1,"method":"x","params":{}}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (disabled auth must fail closed)", resp.StatusCode)
	}
}

// TestRPC_BadEnvelope covers JSON-RPC envelope validation: missing
// jsonrpc field, missing method, malformed JSON.
func TestRPC_BadEnvelope(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("dispatcher reached on envelope error")
			return nil, nil
		},
	}
	srv := newServerForTest(t, fd, "secret")

	cases := []struct {
		name string
		body string
		code int
	}{
		{"malformed_json", `{not json`, -32700},
		{"missing_jsonrpc", `{"id":1,"method":"x"}`, -32600},
		{"wrong_jsonrpc", `{"jsonrpc":"1.0","id":1,"method":"x"}`, -32600},
		{"missing_method", `{"jsonrpc":"2.0","id":1}`, -32600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, srv.ListenerAddr().String(), "secret", tc.body) //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
			out := decodeProblem(t, resp)
			if out.Code != tc.code {
				t.Errorf("problem code = %d, want %d", out.Code, tc.code)
			}
		})
	}
}

// TestProblemShape pins the full RFC 9457 contract (feature 113):
// type is an absolute-path URI reference into the served error
// catalog (/v1/errors#<name>), title is the catalog's canonical
// message, status echoes the HTTP status, and code carries the
// JSON-RPC error code as an extension member.
func TestProblemShape(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("dispatcher reached on auth failure")
			return nil, nil
		},
	}
	srv := newServerForTest(t, fd, "expected")
	resp := postJSON(t, srv.ListenerAddr().String(), "wrong", //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
		`{"jsonrpc":"2.0","id":1,"method":"any","params":{}}`)

	problem := decodeProblem(t, resp)
	if problem.Type != "/v1/errors#unauthorized" {
		t.Errorf("type = %q, want /v1/errors#unauthorized", problem.Type)
	}
	if problem.Title == "" || problem.Title == "error" {
		t.Errorf("title = %q, want the catalog message for unauthorized", problem.Title)
	}
	if problem.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", problem.Status)
	}
	if problem.Code != -32099 {
		t.Errorf("code = %d, want -32099", problem.Code)
	}
}

// TestProblemShape_UncataloguedCode pins the fallback: a code with no
// catalog row still produces a valid problem (generic type + title)
// instead of panicking or emitting an empty type.
func TestProblemShape_UncataloguedCode(t *testing.T) {
	p := newProblem(http.StatusTeapot, -99999, "no such row")
	if p.Type != "/v1/errors" {
		t.Errorf("type = %q, want /v1/errors fallback", p.Type)
	}
	if p.Title != "error" {
		t.Errorf("title = %q, want generic fallback", p.Title)
	}
	if p.Status != http.StatusTeapot || p.Code != -99999 || p.Detail != "no such row" {
		t.Errorf("problem = %+v, want status/code/detail passthrough", p)
	}
}

// TestRPC_DispatcherError pins that a typed dispatcher error
// (codedError interface) propagates the RPCCode into the JSON-RPC
// error envelope on HTTP 200.
func TestRPC_DispatcherError(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, &rpcCodedErr{code: -32100, msg: "policy refused"}
		},
	}
	srv := newServerForTest(t, fd, "secret")
	resp := postJSON(t, srv.ListenerAddr().String(), "secret", //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
		`{"jsonrpc":"2.0","id":99,"method":"send","params":{}}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (typed dispatcher errors live in the envelope)", resp.StatusCode)
	}
	out := decodeResp(t, resp)
	if out.Error == nil {
		t.Fatal("missing error envelope on dispatcher error")
	}
	if out.Error.Code != -32100 {
		t.Errorf("error code = %d, want -32100", out.Error.Code)
	}
}

// TestRPC_BodyTooLarge pins the 1 MiB cap.
func TestRPC_BodyTooLarge(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("dispatcher reached on oversized body")
			return nil, nil
		},
	}
	srv := newServerForTest(t, fd, "secret")
	huge := strings.Repeat("a", maxRequestBodyBytes+1024)
	body := `{"jsonrpc":"2.0","id":1,"method":"x","params":` + string(mustJSON(huge)) + `}`
	resp := postJSON(t, srv.ListenerAddr().String(), "secret", body) //nolint:bodyclose // body closed by postJSON helper via t.Cleanup
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func mustJSON(s string) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestVersion pins the unauthenticated version probe.
func TestVersion(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("dispatcher reached on /v1/version")
			return nil, nil
		},
	}
	srv := newServerForTest(t, fd, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.ListenerAddr().String()+"/v1/version", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("rest-110a")) {
		t.Errorf("body = %q, want adapter version", string(body))
	}
}

// TestNewServer_NilDispatcherRejected and NilAuthRejected pin the
// fail-fast contract on construction.
func TestNewServer_NilDispatcherRejected(t *testing.T) {
	_, err := NewServer(t.Context(), "127.0.0.1:0", nil, NewEnvTokenAuth("x"))
	if err == nil {
		t.Fatal("nil dispatcher accepted; want error")
	}
}

func TestNewServer_NilAuthRejected(t *testing.T) {
	_, err := NewServer(t.Context(), "127.0.0.1:0", &fakeDispatcher{}, nil)
	if err == nil {
		t.Fatal("nil auth accepted; want error")
	}
}
