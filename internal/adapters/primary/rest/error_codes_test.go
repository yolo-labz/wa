package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// rpcRoundTrip posts one JSON-RPC request and returns the decoded response.
// It closes the body in this scope so callers do not have to carry a
// bodyclose suppression, unlike the bare postJSON idiom elsewhere in the
// package.
func rpcRoundTrip(t *testing.T, srv *Server, body string) rpcResponse {
	t.Helper()
	resp := postJSON(t, srv.ListenerAddr().String(), "tok", body)
	defer func() { _ = resp.Body.Close() }()
	return decodeResp(t, resp)
}

// TestRPCSentinelsDoNotCollapseToInternal is the regression guard for the
// bug this file exists to close.
//
// The REST adapter used to map errors with a local codeFromError that only
// did errors.As over the RPCCode interface. Every plain errors.New sentinel
// — an allowlist refusal, an uncached payload, a non-media message — has no
// RPCCode method, so a remote caller got -32603 "internal error" for all of
// them, indistinguishable from a genuine daemon fault. A real triage session
// read that as "WhatsApp expired the media" and chased a resend for a file
// that had never existed.
//
// Each row below reached the wire as -32603 before the fix.
func TestRPCSentinelsDoNotCollapseToInternal(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "unknown message id",
			err:      fmt.Errorf("mediaadapter: 3BB9216134375734E91A: %w", app.ErrMessageNotFound),
			wantCode: -32117,
		},
		{
			name:     "media not cached",
			err:      fmt.Errorf("mediaadapter: 3ADC: %w", domain.ErrMediaNotCached),
			wantCode: -32301,
		},
		{
			name:     "message carries no media",
			err:      fmt.Errorf("mediaadapter: 3ADC: %w", domain.ErrMediaUnsupported),
			wantCode: -32300,
		},
		{
			name:     "allowlist refusal",
			err:      fmt.Errorf("send: %w", app.ErrNotAllowlisted),
			wantCode: -32100,
		},
		{
			name:     "recipient blocked",
			err:      fmt.Errorf("send: %w", domain.ErrBlocked),
			wantCode: -32100,
		},
		{
			name:     "broadcast refused by safety policy",
			err:      fmt.Errorf("send: %w", domain.ErrBroadcastForbidden),
			wantCode: -32100,
		},
		{
			name:     "idempotency collision",
			err:      fmt.Errorf("send: %w", domain.ErrIdempotencyCollision),
			wantCode: -32101,
		},
		{
			name:     "message too large",
			err:      fmt.Errorf("send: %w", domain.ErrMessageTooLarge),
			wantCode: -32201,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := &fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return nil, tc.err
			}}
			srv := newServerForTest(t, fd, "tok")
			got := rpcRoundTrip(t, srv, `{"jsonrpc":"2.0","id":1,"method":"media.download"}`)
			if got.Error == nil {
				t.Fatalf("want an error response, got result %s", got.Result)
			}
			if got.Error.Code == -32603 {
				t.Fatalf("regression: %v reached the wire as -32603 internal error", tc.err)
			}
			if got.Error.Code != tc.wantCode {
				t.Errorf("code = %d, want %d (message %q)", got.Error.Code, tc.wantCode, got.Error.Message)
			}
		})
	}
}

// TestRPCUntypedErrorStaysOpaque pins the other half of the contract: an
// error the daemon did NOT classify must not leak its text. Filesystem
// paths, sqlite errors and upstream detail stay server-side in wad.log.
func TestRPCUntypedErrorStaysOpaque(t *testing.T) {
	secret := "/home/user/.local/share/wa/personal/messages.db"
	fd := &fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, fmt.Errorf("sqlitehistory: open %s: permission denied", secret)
	}}
	srv := newServerForTest(t, fd, "tok")
	got := rpcRoundTrip(t, srv, `{"jsonrpc":"2.0","id":1,"method":"media.download"}`)
	if got.Error == nil {
		t.Fatal("want an error response")
	}
	if got.Error.Code != -32603 {
		t.Errorf("code = %d, want -32603", got.Error.Code)
	}
	if got.Error.Message != "Internal error" {
		t.Errorf("message = %q, want %q", got.Error.Message, "Internal error")
	}
}

// TestRPCCodedErrorKeepsItsCode pins that errors already carrying an
// RPCCode still propagate — the one case the old codeFromError handled,
// which is why -32602 worked over --remote while every sentinel did not.
func TestRPCCodedErrorKeepsItsCode(t *testing.T) {
	fd := &fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, &rpcCodedErr{code: -32602, msg: "invalid params: messageId is required"}
	}}
	srv := newServerForTest(t, fd, "tok")
	got := rpcRoundTrip(t, srv, `{"jsonrpc":"2.0","id":1,"method":"media.download"}`)
	if got.Error == nil {
		t.Fatal("want an error response")
	}
	if got.Error.Code != -32602 {
		t.Errorf("code = %d, want -32602", got.Error.Code)
	}
	if got.Error.Message != "invalid params: messageId is required" {
		t.Errorf("message = %q, want the coded error's own text", got.Error.Message)
	}
}
