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
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// dispatcherCaller adapts rest.Dispatcher to the bridge Caller
// contract. The error goes through app.RPCWire, the same table the socket
// and REST transports use, so /mcp gets the same code, leak-safe message
// and error.data.reason as stdio (FR-116-10); mcpbridge.remediate builds
// the instructive text from that reason alone.
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
			code, msg, reason := app.RPCWire(err)
			data, mErr := json.Marshal(domain.ErrorData{Reason: reason})
			if mErr != nil {
				return nil, nil, mErr
			}
			return nil, &mcpbridge.RPCError{Code: int(code), Message: msg, Data: data}, nil
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
// Scope classes (spec 116 FR-116-11): one server per scope, and the
// tool list is a pure function of it — read gets read tools, send adds
// send tools under the configured send-mode, admin adds admin tools.
//
// Handlers are stateless (no Mcp-Session-Id validation): every tool
// call is self-contained and the daemon's idempotency layer absorbs
// retries, so per-session state would only add reconnect fragility.
func buildMCPProvider(dispatcher rest.Dispatcher, version string, log *slog.Logger) (rest.MCPProvider, error) {
	call := dispatcherCaller(dispatcher)
	sendMode := parseMCPSendMode(os.Getenv("WAD_MCP_SEND_MODE"), log)

	stateless := &mcp.StreamableHTTPOptions{Stateless: true}
	handlers := map[rest.Scope]http.Handler{}
	for _, scope := range []rest.Scope{rest.ScopeReadStr, rest.ScopeSendStr, rest.ScopeAdminStr} {
		srv, err := mcpbridge.NewServer(call, mcpbridge.Config{
			Scope: string(scope), SendMode: sendMode, Version: version,
		})
		if err != nil {
			return nil, err
		}
		handlers[scope] = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, stateless)
	}

	log.Info("mcp http transport enabled at /mcp",
		"sendMode", sendMode, "scopes", "read|send|admin")
	return func(scope rest.Scope) http.Handler {
		return handlers[scope] // unknown scope → nil → rest.handleMCP fails closed (403)
	}, nil
}
