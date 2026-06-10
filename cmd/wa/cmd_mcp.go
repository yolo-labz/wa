package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// Feature 111 M1 — `wa mcp serve`: the stdio MCP server agent runtimes
// (Claude Desktop/Code, Cursor, VS Code, …) spawn per client. It speaks
// MCP on stdin/stdout and forwards every tool call as JSON-RPC to the
// wad daemon socket, so allowlist, rate limits, audit, and the draft
// queue stay enforced in ONE place — the daemon — below the agent
// boundary. stdout is reserved for the protocol; anything human-facing
// goes to stderr (MCP 2025-11-25 permits free stderr logging).

var (
	flagMCPSendMode string
	flagMCPToolsets []string
	flagMCPReadOnly bool
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Model Context Protocol surface",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve MCP over stdio, bridging to the wad daemon socket",
	Long: `Serve the Model Context Protocol over stdio for agent runtimes.

Send-mode (default: draft) decides what send tools do:
  draft   sends become human-review drafts (approve with 'wa draft approve')
  direct  sends go out immediately (allowlist + rate limits still apply)
  deny    send tools are not registered at all

Example Claude Desktop/Code server entry:
  {"command": "wa", "args": ["mcp", "serve"]}`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Fail fast with a human-readable hint when the daemon is down,
		// instead of surfacing the first tool call's transport error.
		if _, _, err := callAndClose(flagSocket, "status", nil); err != nil {
			return exiterr(10, fmt.Errorf("wad daemon unreachable at %s: %w\nhint: start it with `wad` (or `wad install-service`)", flagSocket, err))
		}

		srv, err := mcpbridge.NewServer(socketCaller(flagSocket), mcpbridge.Config{
			SendMode: flagMCPSendMode,
			Toolsets: flagMCPToolsets,
			ReadOnly: flagMCPReadOnly,
			Version:  resolveVersion(),
		})
		if err != nil {
			return exiterr(2, err)
		}
		fmt.Fprintf(os.Stderr, "wa mcp serve: stdio transport up (send-mode=%s read-only=%v socket=%s)\n",
			flagMCPSendMode, flagMCPReadOnly, flagSocket)
		if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			return exiterr(1, fmt.Errorf("mcp serve: %w", err))
		}
		return nil
	},
}

// socketCaller adapts the CLI's one-shot socket helpers to the bridge's
// Caller contract, preserving the JSON-RPC error code for the SEP-1303
// remediation mapping. Dial-per-call keeps the bridge stateless; the
// daemon's idempotency layer absorbs agent-side retries.
func socketCaller(socketPath string) mcpbridge.Caller {
	return func(ctx context.Context, method string, params any) (json.RawMessage, *mcpbridge.RPCError, error) {
		conn, err := dial(socketPath)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = conn.Close() }()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if err := sendHello(conn); err != nil {
			return nil, nil, err
		}
		result, rpcErr, err := call(conn, method, params)
		if err != nil {
			return nil, nil, err
		}
		if rpcErr != nil {
			return nil, &mcpbridge.RPCError{Code: rpcErr.Code, Message: rpcErr.Message}, nil
		}
		return result, nil, nil
	}
}

func init() {
	mcpServeCmd.Flags().StringVar(&flagMCPSendMode, "send-mode", mcpbridge.SendModeDraft,
		"what send tools do: draft|direct|deny")
	mcpServeCmd.Flags().StringSliceVar(&flagMCPToolsets, "toolsets", nil,
		"comma-separated toolsets to expose (messages,contacts,safety); empty = all")
	mcpServeCmd.Flags().BoolVar(&flagMCPReadOnly, "read-only", false,
		"register only read tools, regardless of send-mode")
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
