package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestScopeRefusal_NamesRequiredScope pins F3 / FR-116-10: a 403 scope
// refusal keeps its -32099 wire code and names the scope the method needs
// next to the scope the token holds, on the RPC route and the upload route.
func TestScopeRefusal_NamesRequiredScope(t *testing.T) {
	store := newFakeStore()
	store.Add("ro-tok", ScopeReadStr)
	store.Add("send-tok", ScopeSendStr)
	srv := newScopedServerForTest(t, store)
	addr := srv.ListenerAddr().String()

	cases := []struct {
		name, token, body, required, granted string
	}{
		{"send token on admin method", "send-tok", `{"jsonrpc":"2.0","id":1,"method":"message.edit","params":{}}`, "admin", "send"},
		{"read token on send method", "ro-tok", `{"jsonrpc":"2.0","id":2,"method":"send","params":{}}`, "send", "read"},
		{"self revoke narrows to send", "ro-tok", `{"jsonrpc":"2.0","id":3,"method":"message.revoke","params":{"scope":"self"}}`, "send", "read"},
		{"revoke without self stays admin", "send-tok", `{"jsonrpc":"2.0","id":4,"method":"message.revoke","params":{}}`, "admin", "send"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postScoped(t, addr, tc.token, tc.body)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
			p := decodeProblem(t, resp)
			if p.Code != -32099 || p.Reason != domain.ReasonScopeInsufficient ||
				p.RequiredScope != tc.required || p.GrantedScope != tc.granted {
				t.Fatalf("problem = %+v, want -32099 scope_insufficient required=%s granted=%s", p, tc.required, tc.granted)
			}
		})
	}

	t.Run("upload route", func(t *testing.T) {
		up := newScopedUploadServer(t, store, newFakeMediaWriter(t))
		resp := postUpload(t, up.ListenerAddr().String(), "ro-tok", []byte("x"))
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		p := decodeProblem(t, resp)
		if p.Code != -32099 || p.Reason != domain.ReasonScopeInsufficient || p.RequiredScope != "send" || p.GrantedScope != "read" {
			t.Fatalf("upload problem = %+v", p)
		}
	})
}

// codedNoReason is a coded error that carries no reason (as a test fake
// or a future coded type would): RPCWire must still stamp one.
type codedNoReason struct{}

func (codedNoReason) Error() string { return "not allowlisted" }
func (codedNoReason) RPCCode() int  { return -32100 }

// TestRPCWire_EveryRowCarriesReason walks every RPCWire row (each table
// sentinel and every registered app coded error) plus the untyped and the
// reason-less coded fallbacks, and proves the REST envelope carries
// error.data.reason, a member of the closed enum equal to RPCWire's, on
// every one of them, the internal rows included.
func TestRPCWire_EveryRowCarriesReason(t *testing.T) {
	// The handler runs on the server goroutine; the race detector does not
	// see TCP as a happens-before edge, so the shared error is locked.
	var (
		mu      sync.Mutex
		current error
	)
	srv, err := NewServer(t.Context(), "127.0.0.1:0",
		&fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			return nil, current
		}},
		NewEnvTokenAuth("tok"), WithLogger(slog.New(slog.DiscardHandler)))
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

	rows := append(app.RPCWireErrors(), errors.New("sqlite: disk I/O error"), codedNoReason{})
	if len(rows) < 30 {
		t.Fatalf("RPCWireErrors returned %d rows; the table cannot be that small", len(rows))
	}
	want := map[error]domain.ErrorReason{
		app.ErrNotAllowlisted:           domain.ReasonNotAllowlisted,
		app.ErrMessageNotFound:          domain.ReasonMessageNotFound,
		domain.ErrMediaNotCached:        domain.ReasonMediaNotCached,
		domain.ErrBlocked:               domain.ReasonPolicyRefused,
		rows[len(rows)-2]:               domain.ReasonInternal,
		rows[len(rows)-1]:               domain.ReasonInternal,
		app.ErrTranscriberNotConfigured: domain.ReasonUpstreamError,
	}
	enum := domain.ErrorReasons()
	for i, row := range rows {
		wrapped := fmt.Errorf("wrapped: %w", row)
		mu.Lock()
		current = wrapped
		mu.Unlock()
		code, msg, reason := app.RPCWire(wrapped)
		if reason == "" || !slices.Contains(enum, reason) {
			t.Errorf("row %d (%v): RPCWire reason %q is not in the enum", i, row, reason)
		}
		if r, ok := want[row]; ok && reason != r {
			t.Errorf("row %d (%v): reason %q, want %q", i, row, reason, r)
		}
		resp := postScoped(t, addr, "tok", `{"jsonrpc":"2.0","id":1,"method":"status","params":{}}`)
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var env struct {
			Error *struct {
				Code    int              `json:"code"`
				Message string           `json:"message"`
				Data    domain.ErrorData `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || env.Error == nil {
			t.Fatalf("row %d: envelope %s: %v", i, raw, err)
		}
		if env.Error.Code != int(code) || env.Error.Message != msg || env.Error.Data.Reason != reason {
			t.Errorf("row %d (%v): wire %+v, want (%d, %q, %q)", i, row, env.Error, code, msg, reason)
		}
	}

	// REST-generated refusals follow the same data contract.
	resp := postScoped(t, addr, "wrong", `{"jsonrpc":"2.0","id":1,"method":"status"}`)
	defer func() { _ = resp.Body.Close() }()
	if p := decodeProblem(t, resp); p.Reason != domain.ReasonScopeInsufficient || p.Code != -32099 {
		t.Errorf("401 problem = %+v, want reason scope_insufficient", p)
	}
}

// TestRPC_SendUncertainCarriesTheMessageID: a send that may have been
// delivered reaches a REST client as -32023 send_uncertain with the id in
// error.data.message_id, so it can look the message up instead of resending.
func TestRPC_SendUncertainCarriesTheMessageID(t *testing.T) {
	srv, err := NewServer(t.Context(), "127.0.0.1:0",
		&fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("send: %w", domain.NewSendUncertain("WAID-5", errors.New("ack timeout")))
		}},
		NewEnvTokenAuth("tok"), WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	resp := postScoped(t, srv.ListenerAddr().String(), "tok", `{"jsonrpc":"2.0","id":1,"method":"send","params":{}}`)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code int              `json:"code"`
			Data domain.ErrorData `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope %s: %v", raw, err)
	}
	if env.Error.Code != -32023 || env.Error.Data.Reason != domain.ReasonSendUncertain || env.Error.Data.MessageID != "WAID-5" {
		t.Errorf("wire %s, want -32023 send_uncertain with message_id WAID-5", raw)
	}
}
