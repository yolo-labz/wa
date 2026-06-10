// Feature 111 M2 — Streamable HTTP MCP transport, mounted at /mcp on
// the REST listener and gated by the same bearer-token auth. Tool
// calls run IN-PROCESS against the dispatcher (no socket hop), so the
// allowlist → rate-limit → audit pipeline applies identically to the
// stdio transport. Scope filtering happens at REGISTRATION: each scope
// class gets its own pre-built MCP server, so a read-scope token
// cannot even discover send tools.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// dispatcherCaller adapts rest.Dispatcher to the bridge Caller
// contract. Typed dispatcher errors carry RPCCode (same interface
// handleRPC's codeFromError consumes); the wire message stays generic
// per the REST leak policy — mcpbridge.remediate builds the
// instructive text from the code band, never from internals.
func dispatcherCaller(d rest.Dispatcher) mcpbridge.Caller {
	return func(ctx context.Context, method string, params any) (json.RawMessage, *mcpbridge.RPCError, error) {
		var raw json.RawMessage
		if params != nil {
			b, err := json.Marshal(params)
			if err != nil {
				return nil, nil, err
			}
			raw = b
		}
		result, err := d.Handle(ctx, method, raw)
		if err != nil {
			type rpcCoder interface{ RPCCode() int }
			var coder rpcCoder
			code := -32603
			if errors.As(err, &coder) {
				code = coder.RPCCode()
			}
			msg := "internal error"
			if code != -32603 {
				msg = "rpc error"
			}
			return nil, &mcpbridge.RPCError{Code: code, Message: msg}, nil
		}
		return result, nil, nil
	}
}

// parseMCPSendMode reads WAD_MCP_SEND_MODE. Unset/empty defaults to
// draft (the spec-111 safe default); invalid values fail closed to
// draft with a warning rather than silently going direct.
func parseMCPSendMode(raw string, log *slog.Logger) string {
	switch raw {
	case "":
		return mcpbridge.SendModeDraft
	case mcpbridge.SendModeDirect, mcpbridge.SendModeDraft, mcpbridge.SendModeDeny:
		return raw
	default:
		if log != nil {
			log.Warn("WAD_MCP_SEND_MODE: invalid value, using draft",
				"value", raw, "valid", "direct|draft|deny")
		}
		return mcpbridge.SendModeDraft
	}
}

// mcpDisabled reports the WAD_MCP_DISABLE kill-switch; when set, the
// /mcp route is never registered (checked at the startRESTHTTP call
// site so buildMCPProvider always returns a real provider or an error).
func mcpDisabled() bool {
	v := os.Getenv("WAD_MCP_DISABLE")
	return v == "1" || v == "true"
}

// buildMCPProvider constructs the scope→handler map for rest.WithMCP.
//
// Scope classes (spec 111 FR-111-02):
//
//	read  → read tools only (ReadOnly, every toolset's read surface)
//	send  → full toolset under the configured send-mode
//	admin → same as send (M2 has no admin-only tools)
//
// Handlers are stateless (no Mcp-Session-Id validation): every tool
// call is self-contained and the daemon's idempotency layer absorbs
// retries, so per-session state would only add reconnect fragility.
func buildMCPProvider(dispatcher rest.Dispatcher, version string, log *slog.Logger) (rest.MCPProvider, error) {
	call := dispatcherCaller(dispatcher)
	sendMode := parseMCPSendMode(os.Getenv("WAD_MCP_SEND_MODE"), log)

	readSrv, err := mcpbridge.NewServer(call, mcpbridge.Config{
		ReadOnly: true, Version: version,
	})
	if err != nil {
		return nil, err
	}
	fullSrv, err := mcpbridge.NewServer(call, mcpbridge.Config{
		SendMode: sendMode, Version: version,
	})
	if err != nil {
		return nil, err
	}

	stateless := &mcp.StreamableHTTPOptions{Stateless: true}
	readHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return readSrv }, stateless)
	fullHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fullSrv }, stateless)

	log.Info("mcp http transport enabled at /mcp",
		"sendMode", sendMode, "scopes", "read|send|admin")
	return func(scope rest.Scope) http.Handler {
		switch scope {
		case rest.ScopeReadStr:
			return readHandler
		case rest.ScopeSendStr, rest.ScopeAdminStr:
			return fullHandler
		default:
			return nil // unknown scope — rest.handleMCP fails closed (403)
		}
	}, nil
}
