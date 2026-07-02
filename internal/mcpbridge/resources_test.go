package mcpbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// resourceURIs collects the URIs of all static resources registered on srv.
func resourceURIs(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	for r, err := range cs.Resources(context.Background(), nil) {
		if err != nil {
			t.Fatalf("Resources iterator: %v", err)
		}
		out[r.URI] = true
	}
	return out
}

// templateURIs collects the URI templates of all registered resource templates.
func templateURIs(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	for rt, err := range cs.ResourceTemplates(context.Background(), nil) {
		if err != nil {
			t.Fatalf("ResourceTemplates iterator: %v", err)
		}
		out[rt.URITemplate] = true
	}
	return out
}

// TestResourceRegistration_Scoping pins which resources appear under each
// toolset configuration — mirroring the scope contract of the equivalent tools.
func TestResourceRegistration_Scoping(t *testing.T) {
	t.Parallel()

	t.Run("all toolsets: all three resources registered", func(t *testing.T) {
		t.Parallel()
		cs := session(t, &fakeCaller{}, Config{})
		uris := resourceURIs(t, cs)
		tmpls := templateURIs(t, cs)
		if !uris["wa://chats/recent"] {
			t.Error("wa://chats/recent missing with all toolsets")
		}
		if !uris["wa://status"] {
			t.Error("wa://status missing with all toolsets")
		}
		if !tmpls["wa://contact/{jid}"] {
			t.Error("wa://contact/{jid} template missing with all toolsets")
		}
	})

	t.Run("contacts only: chats and contact registered, no status", func(t *testing.T) {
		t.Parallel()
		cs := session(t, &fakeCaller{}, Config{Toolsets: []string{ToolsetContacts}})
		uris := resourceURIs(t, cs)
		tmpls := templateURIs(t, cs)
		if !uris["wa://chats/recent"] {
			t.Error("wa://chats/recent missing with contacts toolset")
		}
		if !tmpls["wa://contact/{jid}"] {
			t.Error("wa://contact/{jid} template missing with contacts toolset")
		}
		if uris["wa://status"] {
			t.Error("wa://status must not appear without meta toolset")
		}
	})

	t.Run("meta only: status registered, no chats or contact", func(t *testing.T) {
		t.Parallel()
		cs := session(t, &fakeCaller{}, Config{Toolsets: []string{ToolsetMeta}})
		uris := resourceURIs(t, cs)
		tmpls := templateURIs(t, cs)
		if !uris["wa://status"] {
			t.Error("wa://status missing with meta toolset")
		}
		if uris["wa://chats/recent"] {
			t.Error("wa://chats/recent must not appear without contacts toolset")
		}
		if tmpls["wa://contact/{jid}"] {
			t.Error("wa://contact/{jid} must not appear without contacts toolset")
		}
	})

	t.Run("read-only does not suppress inherently-read-only resources", func(t *testing.T) {
		t.Parallel()
		cs := session(t, &fakeCaller{}, Config{ReadOnly: true})
		uris := resourceURIs(t, cs)
		if !uris["wa://chats/recent"] || !uris["wa://status"] {
			t.Error("--read-only must not suppress resources (they are always read-only)")
		}
	})
}

// TestResourceReadRoundTrip confirms each resource handler forwards the
// correct daemon RPC and surfaces the result as JSON text content.
func TestResourceReadRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("wa://chats/recent forwards to chat.list", func(t *testing.T) {
		t.Parallel()
		fc := &fakeCaller{result: json.RawMessage(`{"chats":[]}`)}
		cs := session(t, fc, Config{})
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "wa://chats/recent"})
		if err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
		if got := fc.last(t).method; got != "chat.list" {
			t.Fatalf("forwarded %q, want chat.list", got)
		}
		if len(res.Contents) == 0 || res.Contents[0].Text == "" {
			t.Error("expected non-empty JSON text in resource contents")
		}
	})

	t.Run("wa://contact/{jid} forwards to contacts.search with extracted jid", func(t *testing.T) {
		t.Parallel()
		fc := &fakeCaller{result: json.RawMessage(`{"contacts":[]}`)}
		cs := session(t, fc, Config{})
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
			URI: "wa://contact/5511999999999",
		})
		if err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
		last := fc.last(t)
		if last.method != "contacts.search" {
			t.Fatalf("forwarded %q, want contacts.search", last.method)
		}
		// Bare phone input is canonicalised through domain.Parse before
		// crossing the bridge (P2 hardening: only canonical JIDs forward).
		if last.params["query"] != "5511999999999@s.whatsapp.net" {
			t.Errorf("query param = %v, want canonical 5511999999999@s.whatsapp.net", last.params["query"])
		}
		if len(res.Contents) == 0 {
			t.Error("expected resource contents")
		}
	})

	t.Run("wa://status forwards to status", func(t *testing.T) {
		t.Parallel()
		fc := &fakeCaller{result: json.RawMessage(`{"state":"connected"}`)}
		cs := session(t, fc, Config{})
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "wa://status"})
		if err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
		if got := fc.last(t).method; got != "status" {
			t.Fatalf("forwarded %q, want status", got)
		}
		if !strings.Contains(res.Contents[0].Text, "connected") {
			t.Errorf("status text missing 'connected': %q", res.Contents[0].Text)
		}
	})

	t.Run("percent-encoded JID is decoded before forwarding", func(t *testing.T) {
		t.Parallel()
		fc := &fakeCaller{result: json.RawMessage(`{"contacts":[]}`)}
		cs := session(t, fc, Config{})
		// @ is percent-encoded as %40 in the URI
		_, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
			URI: "wa://contact/5511999999999%40s.whatsapp.net",
		})
		if err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
		if got := fc.last(t).params["query"]; got != "5511999999999@s.whatsapp.net" {
			t.Errorf("decoded JID = %v, want 5511999999999@s.whatsapp.net", got)
		}
	})
}

// TestResourceTemplate_URIExtraction pins jidFromContactURI against
// valid, percent-encoded, and invalid inputs.
func TestResourceTemplate_URIExtraction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uri     string
		wantJID string
		wantErr bool
	}{
		{"wa://contact/5511999999999", "5511999999999@s.whatsapp.net", false}, // phone → canonical
		{"wa://contact/5511999999999%40s.whatsapp.net", "5511999999999@s.whatsapp.net", false},
		{"wa://contact/", "", true},                                              // empty JID rejected at the bridge (P2)
		{"wa://other/x", "", true},                                               //
		{"wa://contacts/5511999999999", "", true},                                // wrong path
		{"wa://contact/..%2F..%2Fetc", "", true},                                 // path traversal: decoded "/" rejected
		{"wa://contact/5511999999999%2540s.whatsapp.net", "", true},              // double-encode: residual "%" rejected (would mis-resolve via phone normalisation)
		{"wa://contact/not-a-jid@nowhere.example", "", true},                     // unknown server → domain.Parse rejects
		{"wa://contact/1234567890@broadcast", "", true},                          // broadcast forbidden by safety policy
		{"wa://contact/not-a-jid12345678", "", true},                             // digit noise: phone normalisation would mis-resolve → rejected
		{"wa://contact/%2B5511999999999", "5511999999999@s.whatsapp.net", false}, // "+"-prefixed phone (encoded) is valid
	}
	for _, tc := range cases {
		jid, err := jidFromContactURI(tc.uri)
		if (err != nil) != tc.wantErr {
			t.Errorf("jidFromContactURI(%q): err=%v wantErr=%v", tc.uri, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && jid != tc.wantJID {
			t.Errorf("jidFromContactURI(%q) = %q, want %q", tc.uri, jid, tc.wantJID)
		}
	}
}
