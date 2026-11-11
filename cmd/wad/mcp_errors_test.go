package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket/sockettest"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// reasonedErr is a coded error with its own reason, the shape of app's
// typed errors, used to put both -32099 refusals on the wire.
type reasonedErr struct {
	code   int
	msg    string
	reason domain.ErrorReason
}

func (e *reasonedErr) Error() string                 { return e.msg }
func (e *reasonedErr) RPCCode() int                  { return e.code }
func (e *reasonedErr) RPCReason() domain.ErrorReason { return e.reason }

// plainCoded is a coded error with no reason at all.
type plainCoded struct {
	code int
	msg  string
}

func (e *plainCoded) Error() string { return e.msg }
func (e *plainCoded) RPCCode() int  { return e.code }

type collisionCase struct {
	err    error
	code   int
	reason domain.ErrorReason
	remedy string
}

// collisionCases share a code pairwise, and every message points at a
// reason other than the one on the wire.
var collisionCases = map[string]collisionCase{
	"not-allowlisted": {app.ErrNotAllowlisted, -32100, domain.ReasonNotAllowlisted, "wa allow add"},
	"blocked":         {fmt.Errorf("send: not allowlisted: %w", domain.ErrBlocked), -32100, domain.ReasonPolicyRefused, "safety policy"},
	"scope":           {&reasonedErr{-32099, "media not found", domain.ReasonScopeInsufficient}, -32099, domain.ReasonScopeInsufficient, "token's scope"},
	"media":           {&reasonedErr{-32099, "scope insufficient for method", domain.ReasonMediaNotFound}, -32099, domain.ReasonMediaNotFound, "media object"},
	"absent-reason":   {&plainCoded{-32100, "not allowlisted"}, -32100, domain.ReasonInternal, "Internal daemon error"},
	"unknown-reason":  {&reasonedErr{-32099, "scope insufficient for method", "bogus"}, -32099, domain.ReasonInternal, "Internal daemon error"},
}

// collisionHandler refuses thread.get with the case named by params.chat.
func collisionHandler(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		Chat string `json:"chat"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	c, ok := collisionCases[p.Chat]
	if !ok {
		return nil, errors.New("unknown case")
	}
	return nil, c.err
}

type handlerDispatcher struct{ h sockettest.HandlerFunc }

func (d handlerDispatcher) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return d.h(ctx, method, params)
}

// httpSession boots the production /mcp composition (REST listener, scoped
// auth, buildMCPProvider) and connects an SDK Streamable HTTP client.
func httpSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return mcpHTTPSession(t, handlerDispatcher{collisionHandler}, rest.ScopeSendStr)
}

// mcpHTTPSession is httpSession over dispatcher d with a token of scope.
func mcpHTTPSession(t *testing.T, d rest.Dispatcher, scope rest.Scope) *mcp.ClientSession {
	t.Helper()
	return connectMCP(t, mcpHTTPServer(t, d, map[string]rest.Scope{"tok": scope}), "tok")
}

// mcpHTTPServer serves /mcp over d for tokens and returns its address.
func mcpHTTPServer(t *testing.T, d rest.Dispatcher, tokens map[string]rest.Scope) string {
	t.Helper()
	provider, err := buildMCPProvider(d, "test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildMCPProvider: %v", err)
	}
	srv, err := rest.NewServer(t.Context(), "127.0.0.1:0", d,
		rest.NewScopedAuth(&mcpScopeStore{tokens: tokens}),
		rest.WithLogger(slog.New(slog.DiscardHandler)), rest.WithMCP(provider))
	if err != nil {
		t.Fatalf("rest.NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("rest shutdown: %v", err)
		}
	})
	return srv.ListenerAddr().String()
}

// socketRPC is what `wa mcp serve`'s socketCaller does: one connection per
// call, system.hello, one request, and the error object passed to the
// bridge with its data untouched.
func socketRPC(path string) mcpbridge.Caller {
	return func(ctx context.Context, method string, params any) (json.RawMessage, *mcpbridge.RPCError, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		br := bufio.NewReader(conn)
		roundTrip := func(req any) (json.RawMessage, *mcpbridge.RPCError, error) {
			b, err := json.Marshal(req)
			if err != nil {
				return nil, nil, err
			}
			if _, err := conn.Write(append(b, '\n')); err != nil {
				return nil, nil, err
			}
			line, err := br.ReadBytes('\n')
			if err != nil {
				return nil, nil, err
			}
			var resp struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int             `json:"code"`
					Message string          `json:"message"`
					Data    json.RawMessage `json:"data"`
				} `json:"error"`
			}
			if err := json.Unmarshal(line, &resp); err != nil {
				return nil, nil, err
			}
			if resp.Error != nil {
				return nil, &mcpbridge.RPCError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}, nil
			}
			return resp.Result, nil, nil
		}
		hello := map[string]any{"jsonrpc": "2.0", "id": 0, "method": "system.hello", "params": map[string]int{"protoVersion": domain.ProtoVersion}}
		if _, rpcErr, err := roundTrip(hello); err != nil {
			return nil, nil, fmt.Errorf("hello: %w", err)
		} else if rpcErr != nil {
			return nil, nil, fmt.Errorf("hello refused: %d %s", rpcErr.Code, rpcErr.Message)
		}
		return roundTrip(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	}
}

// socketSession serves the dispatcher on a real unix socket and connects a
// stdio-shaped bridge to it through socketRPC.
func socketSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	fd := sockettest.NewFakeDispatcher()
	fd.On("thread.get", collisionHandler)
	path := sockettest.TempSocketPath(t)
	server := socket.NewServer(fd, slog.New(slog.DiscardHandler), socket.WithShutdownDeadline(2*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, path) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("socket server: %v", err)
		}
		fd.Close()
	})
	sockettest.WaitForSocket(t, path, 3*time.Second)

	bridge, err := mcpbridge.NewServer(socketRPC(path), mcpbridge.Config{})
	if err != nil {
		t.Fatalf("mcpbridge.NewServer: %v", err)
	}
	st, ct := mcp.NewInMemoryTransports()
	bctx, bcancel := context.WithCancel(context.Background())
	bdone := make(chan struct{})
	go func() { defer close(bdone); _ = bridge.Run(bctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "collision", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close(); bcancel(); <-bdone })
	return cs
}

// TestRemediate_CodeCollisionsBothTransports pins FR-116-10's collision
// rule on the stdio/socket bridge and on Streamable HTTP: -32100 covers
// not-allowlisted and other policy refusals, -32099 covers scope and media
// refusals, and each message is written to mislead. The agent sees the
// wire reason's remediation on both transports, and an absent or unknown
// reason is internal.
func TestRemediate_CodeCollisionsBothTransports(t *testing.T) {
	transports := map[string]func(*testing.T) *mcp.ClientSession{"socket": socketSession, "http": httpSession}
	for tname, open := range transports {
		t.Run(tname, func(t *testing.T) {
			cs := open(t)
			for chat, want := range collisionCases {
				res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "wa_get_thread", Arguments: map[string]any{"chat": chat}})
				if err != nil {
					t.Fatalf("%s: protocol error %v, want a tool error", chat, err)
				}
				raw, _ := json.Marshal(res.StructuredContent)
				var got mcpbridge.ToolError
				if err := json.Unmarshal(raw, &got); err != nil || !res.IsError {
					t.Fatalf("%s: IsError=%v structured %s: %v", chat, res.IsError, raw, err)
				}
				if got.Code != want.code || got.Reason != want.reason || !strings.Contains(got.Remediation, want.remedy) {
					t.Errorf("%s over %s: got %+v, want code %d reason %s remedy ~%q", chat, tname, got, want.code, want.reason, want.remedy)
				}
				if want.reason != domain.ReasonNotAllowlisted && strings.Contains(got.Remediation, "allowlist") {
					t.Errorf("%s over %s: remediation %q follows the misleading message", chat, tname, got.Remediation)
				}
			}
		})
	}
}
