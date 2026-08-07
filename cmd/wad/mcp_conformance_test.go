package main

// Feature 111 M2 (spec 111 §Milestones M2) — Streamable-HTTP conformance
// round-trip proof. The M2 surface is implemented (mcp_http.go mounts the
// go-sdk StreamableHTTPHandler at /mcp behind rest.handleMCP's auth +
// scope gate, rest/server.go:210,377-394), but nothing exercised
// initialize → tools/list → tools/call over real HTTP with an official
// SDK client — the transport was construction-tested only. This test is
// that round trip:
//
//  1. handshake — initialize over real TCP POST to /mcp; the server
//     identifies as "wa" with a protocol version.
//  2. tools/list — a read-scope token sees ONLY the read toolset
//     (registration IS the FR-111-02 filter); a send-scope token sees
//     the full toolset including the three M2 additions
//     (wa_group_info, wa_draft_review, wa_status).
//  3. tools/call — wa_status round-trips the dispatcher result through
//     HTTP; the same tool under a read-scope token is refused with
//     "unknown tool" (undiscoverable AND uncallable).
//
// Uses the go-sdk's official client-side StreamableClientTransport, so
// the wire format is validated against the SDK's own expectations, not
// a hand-rolled request.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
)

// mcpScopeStore is a rest.TokenStore granting a fixed scope per token
// (the spec 110d shape — same contract as rest.NewScopedAuth expects).
type mcpScopeStore struct {
	tokens map[string]rest.Scope
}

func (s *mcpScopeStore) Verify(_ context.Context, raw string) (rest.Scope, string, error) {
	scope, ok := s.tokens[raw]
	if !ok {
		return "", "", errors.New("unknown token")
	}
	return scope, "tok-" + string(scope), nil
}

// bearerTransport injects the bearer token on every request. The SDK
// client has no auth hook, so the header lands in the RoundTripper —
// which is exactly the seam a real deployment uses (httputil reverse
// proxy, tailscale, etc.).
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}

// newMCPConformanceServer boots the full production composition — rest
// listener + scoped auth + buildMCPProvider — on a real TCP port and
// returns its address. The dispatcher is a stub returning a canned
// result; this test proves transport + handshake + scope filtering, not
// dispatcher behaviour (covered by the socket/REST suites).
func newMCPConformanceServer(t *testing.T, store rest.TokenStore) string {
	t.Helper()
	dispatcher := &stubRESTDispatcher{result: json.RawMessage(`{"ok":true}`)}
	provider, err := buildMCPProvider(dispatcher, "test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildMCPProvider: %v", err)
	}
	srv, err := rest.NewServer(t.Context(), "127.0.0.1:0", dispatcher,
		rest.NewScopedAuth(store),
		rest.WithLogger(slog.New(slog.DiscardHandler)),
		rest.WithMCP(provider))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv.ListenerAddr().String()
}

// connectMCP performs the MCP initialize handshake over Streamable HTTP
// and returns the session. DisableStandaloneSSE is required: the /mcp
// handler is stateless (buildMCPProvider sets Stateless), so the client
// must not open the optional GET SSE stream. MaxRetries is disabled for
// deterministic tests.
func connectMCP(t *testing.T, addr, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "wa-conformance-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: "http://" + addr + "/mcp",
		HTTPClient: &http.Client{
			// No Client.Timeout on purpose: a non-zero timeout makes
			// net/http wrap each response in a cancelTimerBody whose
			// stop fires only on body-close/EOF, and the SDK client
			// does not drain to EOF — the per-request timer goroutine
			// would outlive the test and trip goleak. The handshake
			// is bounded by the ctx passed to Connect (10s above) and
			// every session call by t.Context(); the server side has
			// its own Read/WriteTimeouts (rest.NewServer).
			Transport: &bearerTransport{base: http.DefaultTransport, token: token},
		},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolSet(t *testing.T, session *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestMCPStreamableHTTP_Conformance(t *testing.T) {
	// Hermetic: whatever the host env says, the full toolset registers.
	t.Setenv("WAD_MCP_SEND_MODE", "direct")

	const (
		readTok = "tok-read-read-read-read-read-read-read-0001"
		sendTok = "tok-send-send-send-send-send-send-send-0001"
	)
	store := &mcpScopeStore{tokens: map[string]rest.Scope{
		readTok: rest.ScopeReadStr,
		sendTok: rest.ScopeSendStr,
	}}
	addr := newMCPConformanceServer(t, store)

	t.Run("initialize handshake over HTTP", func(t *testing.T) {
		session := connectMCP(t, addr, sendTok)
		init := session.InitializeResult()
		if init == nil {
			t.Fatal("no initialize result")
		}
		if init.ServerInfo.Name != "wa" {
			t.Errorf("serverInfo.name = %q, want %q", init.ServerInfo.Name, "wa")
		}
		if init.ProtocolVersion == "" {
			t.Error("initialize result carries no protocolVersion")
		}
	})

	t.Run("read-scope token gets the filtered toolset", func(t *testing.T) {
		session := connectMCP(t, addr, readTok)
		names := toolSet(t, session)
		for _, want := range []string{
			"wa_status", "wa_group_info", "wa_draft_review",
			"wa_list_chats", "wa_get_thread", "wa_search_messages", "wa_resolve_contact",
		} {
			if !names[want] {
				t.Errorf("read toolset missing %s", want)
			}
		}
		for _, forbid := range []string{"wa_send_message", "wa_send_media", "wa_schedule_message"} {
			if names[forbid] {
				t.Errorf("read toolset must NOT expose %s (FR-111-02 registration filtering)", forbid)
			}
		}
	})

	t.Run("send-scope token gets the full toolset", func(t *testing.T) {
		session := connectMCP(t, addr, sendTok)
		names := toolSet(t, session)
		for _, want := range []string{
			"wa_status", "wa_group_info", "wa_draft_review",
			"wa_send_message", "wa_send_media", "wa_schedule_message",
		} {
			if !names[want] {
				t.Errorf("send toolset missing %s", want)
			}
		}
	})

	t.Run("tools/call round-trips the dispatcher result over HTTP", func(t *testing.T) {
		session := connectMCP(t, addr, sendTok)
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "wa_status", Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("call wa_status: %v", err)
		}
		if res.IsError {
			t.Fatalf("wa_status returned IsError=true: %+v", res)
		}
		// The client decodes StructuredContent generically; re-marshal and
		// compare against the stub dispatcher's canned result.
		got, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal StructuredContent: %v", err)
		}
		if string(got) != `{"ok":true}` {
			t.Errorf("StructuredContent = %s, want the stub dispatcher's canned result", got)
		}
	})

	t.Run("read-scope token cannot call send tools", func(t *testing.T) {
		session := connectMCP(t, addr, readTok)
		_, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "wa_send_message",
			Arguments: map[string]any{"to": "5511999999999", "body": "hi"},
		})
		if err == nil {
			t.Fatal("wa_send_message under a read-scope token must fail")
		}
		if !strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("refusal error = %q, want JSON-RPC 'unknown tool'", err)
		}
	})
}
