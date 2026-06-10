// Package mcpbridge implements feature 111 M1: the stdio MCP server
// that `wa mcp serve` runs per-client. It registers workflow-shaped
// tools (spec 111 §Decision) and forwards every call as JSON-RPC to
// the wad daemon, so the daemon's allowlist → rate-limit → audit
// pipeline stays the single policy enforcement point. The bridge holds
// no policy of its own except tool REGISTRATION (send-mode, toolsets,
// read-only): what is not registered does not exist for the agent.
package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RPCError is the daemon-side JSON-RPC error surfaced to the bridge.
// Code drives the remediation mapping in errors.go.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc %d: %s", e.Code, e.Message)
}

// Caller forwards one JSON-RPC method call to the daemon. rpcErr is
// non-nil for daemon-side refusals (policy, rate limit, upstream);
// err is non-nil for transport faults (socket gone, handshake).
type Caller func(ctx context.Context, method string, params any) (result json.RawMessage, rpcErr *RPCError, err error)

// Send modes (spec 111 FR-111-03). Draft is the default: send tools
// enqueue human-review drafts instead of sending directly.
const (
	SendModeDirect = "direct"
	SendModeDraft  = "draft"
	SendModeDeny   = "deny"
)

// Toolset names. M1 shipped messages + contacts + safety (read-only
// draft review); M2 adds groups + meta (both read-only surfaces).
const (
	ToolsetMessages = "messages"
	ToolsetContacts = "contacts"
	ToolsetSafety   = "safety"
	ToolsetGroups   = "groups"
	ToolsetMeta     = "meta"
)

var allToolsets = []string{ToolsetMessages, ToolsetContacts, ToolsetSafety, ToolsetGroups, ToolsetMeta}

// Config selects which tools the agent sees.
type Config struct {
	// SendMode is direct|draft|deny (default draft, the spec-111 safe
	// default: agent proposes, human disposes).
	SendMode string
	// Toolsets whitelists tool groups; empty = all M1 toolsets.
	Toolsets []string
	// ReadOnly drops every mutating tool regardless of SendMode.
	ReadOnly bool
	// Version stamps the MCP server implementation info.
	Version string
}

// normalize validates and defaults the config.
func (c *Config) normalize() error {
	switch c.SendMode {
	case "":
		c.SendMode = SendModeDraft
	case SendModeDirect, SendModeDraft, SendModeDeny:
	default:
		return fmt.Errorf("invalid send-mode %q (want direct|draft|deny)", c.SendMode)
	}
	if len(c.Toolsets) == 0 {
		c.Toolsets = slices.Clone(allToolsets)
		return nil
	}
	for _, ts := range c.Toolsets {
		if !slices.Contains(allToolsets, ts) {
			return fmt.Errorf("unknown toolset %q (want %s)", ts, strings.Join(allToolsets, "|"))
		}
	}
	return nil
}

// NewServer builds the MCP server with the tool set implied by cfg.
func NewServer(call Caller, cfg Config) (*mcp.Server, error) {
	if call == nil {
		return nil, errors.New("mcpbridge: nil caller")
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "wa",
		Title:   "wa — safety-first WhatsApp daemon",
		Version: cfg.Version,
	}, nil)
	registerTools(srv, call, cfg)
	return srv, nil
}

// has reports whether the toolset is enabled.
func (c Config) has(toolset string) bool {
	return slices.Contains(c.Toolsets, toolset)
}

// mutating reports whether mutating tools are allowed at all.
func (c Config) mutating() bool {
	return !c.ReadOnly && c.SendMode != SendModeDeny
}
