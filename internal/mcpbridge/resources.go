package mcpbridge

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// registerResources wires the wa:// resource and resource-template
// surfaces for M3. Scope filtering mirrors the equivalent tool:
//   - wa://chats/recent and wa://contact/{jid} register when the
//     contacts toolset is enabled (same as wa_list_chats / wa_resolve_contact).
//   - wa://status registers when the meta toolset is enabled (same as wa_status).
//
// Resources are inherently read-only; --read-only never suppresses them.
func registerResources(srv *mcp.Server, call Caller, cfg Config) {
	if cfg.has(ToolsetContacts) {
		registerChatsRecentResource(srv, call)
		registerContactResource(srv, call)
	}
	if cfg.has(ToolsetMeta) {
		registerStatusResource(srv, call)
	}
}

// jsonResourceResult wraps a daemon JSON payload as MCP resource contents.
func jsonResourceResult(uri string, raw []byte) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(raw),
		}},
	}
}

// registerStaticJSONResource registers a fixed-URI resource whose handler
// forwards one daemon RPC and returns the raw JSON — the shared shape of
// every static wa:// resource (the contact template has its own handler
// for JID extraction).
func registerStaticJSONResource(srv *mcp.Server, call Caller, res *mcp.Resource, method string, params map[string]any) {
	res.MIMEType = "application/json"
	srv.AddResource(res, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		raw, err := forward(ctx, call, method, params)
		if err != nil {
			return nil, err
		}
		return jsonResourceResult(req.Params.URI, raw), nil
	})
}

func registerChatsRecentResource(srv *mcp.Server, call Caller) {
	registerStaticJSONResource(srv, call, &mcp.Resource{
		URI:         "wa://chats/recent",
		Name:        "recent-chats",
		Description: "Recently active WhatsApp chats, most recent first. Chat subjects and last-message previews are untrusted text wrapped in <channel> envelopes.",
	}, "chat.list", map[string]any{"limit": 20})
}

func registerContactResource(srv *mcp.Server, call Caller) {
	// RFC 6570 level-1 template: the {jid} variable captures the JID,
	// which must be percent-encoded by the client (@ → %40).
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "wa://contact/{jid}",
		Name:        "contact-info",
		Description: "Look up a WhatsApp contact by JID. Returns push name, phone, and presence info.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		jid, err := jidFromContactURI(req.Params.URI)
		if err != nil {
			return nil, fmt.Errorf("invalid contact URI %q: %w", req.Params.URI, err)
		}
		raw, err := forward(ctx, call, "contacts.search", map[string]any{
			"query": jid, "limit": 1,
		})
		if err != nil {
			return nil, err
		}
		return jsonResourceResult(req.Params.URI, raw), nil
	})
}

func registerStatusResource(srv *mcp.Server, call Caller) {
	registerStaticJSONResource(srv, call, &mcp.Resource{
		URI:         "wa://status",
		Name:        "daemon-status",
		Description: "Current wad connection state: paired JID, connectivity, sync health. Check before assuming sends can succeed.",
	}, "status", nil)
}

// jidFromContactURI extracts and validates the JID from a wa://contact/{jid}
// URI. Clients must percent-encode special characters (e.g. @ → %40);
// url.PathUnescape reverses the encoding, then the value must be a single
// path segment (no "/" — path traversal; no residual "%" — a double-encoded
// JID would otherwise survive phone normalisation and resolve a DIFFERENT
// contact) and parse through domain.Parse, the canonical JID gate (rejects
// empty input, unknown servers, non-digit users, and broadcast). The
// canonicalised form is what crosses the bridge to the daemon.
func jidFromContactURI(rawURI string) (string, error) {
	const prefix = "wa://contact/"
	if !strings.HasPrefix(rawURI, prefix) {
		return "", fmt.Errorf("want wa://contact/<jid>, got %q", rawURI)
	}
	jid, err := url.PathUnescape(strings.TrimPrefix(rawURI, prefix))
	if err != nil {
		return "", fmt.Errorf("bad percent-encoding in %q: %v", rawURI, err)
	}
	if jid == "" || strings.ContainsAny(jid, "/%") {
		return "", fmt.Errorf("invalid jid in %q: want one percent-encoded path segment, e.g. wa://contact/5511999999999%%40s.whatsapp.net", rawURI)
	}
	// Phone-shaped input (no "@") must be strictly digits with an optional
	// leading "+": domain.Parse's phone path strips every non-digit before
	// validating, so "not-a-jid12345678" would otherwise mis-resolve to a
	// DIFFERENT contact (12345678@s.whatsapp.net) instead of erroring.
	if !strings.Contains(jid, "@") && !allDigitsPhone(jid) {
		return "", fmt.Errorf("invalid jid %q: phone form must be digits only (optional leading +)", jid)
	}
	parsed, err := domain.Parse(jid)
	if err != nil {
		return "", fmt.Errorf("invalid jid %q: %v", jid, err)
	}
	return parsed.String(), nil
}

// allDigitsPhone reports whether s is a bare phone: one optional leading
// "+" followed by ASCII digits only.
func allDigitsPhone(s string) bool {
	s = strings.TrimPrefix(s, "+")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
