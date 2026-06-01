package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuffer is a concurrency-safe io.Writer for capturing slog output:
// the handler goroutine writes the recover log while the test goroutine
// reads it. Plain bytes.Buffer would trip the race detector.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestHandleRPC_PanicRecoveredWithStack pins the recover-path hardening.
// A dispatcher that nil-pointer-derefs — the same panic class that fired
// twice on wa-burocracy 2026-06-01 ("rest: panic in dispatcher: nil
// pointer dereference") with no stack, so it was undiagnosable — must:
//
//   - NOT crash the HTTP server: the connection returns a controlled
//     JSON-RPC error envelope (HTTP 500, code -32603);
//   - log the panic WITH a stack trace, so the next occurrence names the
//     offending method + nil path instead of being a blind dead end.
func TestHandleRPC_PanicRecoveredWithStack(t *testing.T) {
	fd := &fakeDispatcher{
		handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			var p *int
			_ = *p // nil pointer dereference — mirrors the prod panic class
			return nil, nil
		},
	}

	var logBuf safeBuffer
	srv, err := NewServer(t.Context(), "127.0.0.1:0", fd, NewEnvTokenAuth("tok"),
		WithLogger(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	resp := postJSON(t, srv.ListenerAddr().String(), "tok", //nolint:bodyclose // closed via postJSON t.Cleanup
		`{"jsonrpc":"2.0","id":1,"method":"anything"}`)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (panic recovered into an envelope)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	var env struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("response is not a JSON-RPC envelope: %v (body=%s)", err, body)
	}
	if env.Error.Code != -32603 {
		t.Errorf("error.code = %d, want -32603", env.Error.Code)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "rest: panic in dispatcher") {
		t.Errorf("recover was not logged; got %q", logged)
	}
	// The stack is the whole point of the fix. debug.Stack() always opens
	// with "goroutine N [running]:" — its presence proves the trace was
	// captured (vs the old panic-value-only log).
	if !strings.Contains(logged, "stack=") || !strings.Contains(logged, "goroutine ") {
		t.Errorf("recover log is missing the stack trace; got %q", logged)
	}
}
