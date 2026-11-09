package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	mediaChat = "5511900000001"
	mediaMsg  = "3EB0MEDIA"
)

// mediaStub is the daemon's media methods behind /mcp, recording calls.
type mediaStub struct {
	mu    sync.Mutex
	calls []string
}

func (m *mediaStub) handler(method string) compositionHandler {
	return func(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
		m.mu.Lock()
		m.calls = append(m.calls, method+" "+string(params))
		m.mu.Unlock()
		switch method {
		case "media.list":
			return json.RawMessage(`{"media":[{"messageId":"` + mediaMsg + `","chatJid":"` + mediaChat + `","timestamp":1}],"appliedFilters":["chat","sender"]}`), nil
		case "media.download":
			return json.RawMessage(`{"object":{"sha256":"ab12","mime":"image/png","size":3}}`), nil
		default: // media.fetchBytes
			return json.RawMessage(`{"bytes":"AQID","eof":true}`), nil
		}
	}
}

func (m *mediaStub) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.calls
	m.calls = nil
	return out
}

// dispatcher routes the media methods, wrapping list/download in the
// production audit interceptor exactly as main.go does.
func (m *mediaStub) dispatcher(audit *recordingAudit) rest.Dispatcher {
	routes := map[string]compositionHandler{
		"media.list":       auditMCPMedia("media.list", m.handler("media.list"), audit),
		"media.download":   auditMCPMedia("media.download", m.handler("media.download"), audit),
		"media.fetchBytes": m.handler("media.fetchBytes"),
	}
	return handlerDispatcher{func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		h, ok := routes[method]
		if !ok {
			return nil, errors.New("unexpected " + method)
		}
		return h(ctx, params)
	}}
}

func listHandle(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_list_media", Arguments: map[string]any{"chat": mediaChat, "sender": mediaChat},
	})
	if err != nil || res.IsError {
		t.Fatalf("wa_list_media: %v %+v", err, res)
	}
	var out struct {
		Media []struct {
			Handle string `json:"media_handle"`
		} `json:"media"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if json.Unmarshal(raw, &out) != nil || len(out.Media) != 1 || out.Media[0].Handle == "" {
		t.Fatalf("list result %s", raw)
	}
	if strings.Contains(string(raw), "messageId") {
		t.Errorf("list exposes raw message ids: %s", raw)
	}
	return out.Media[0].Handle
}

func getMedia(t *testing.T, cs *mcp.ClientSession, handle string) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "wa_get_media", Arguments: map[string]any{"media_handle": handle}})
	if err != nil {
		t.Fatalf("wa_get_media: %v", err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	return res, string(raw)
}

// TestMCPMedia_HandleBindsTokenAndRow: over /mcp, a handle fetches only the
// row it was listed for and only for the token that listed it; another
// token, a raw id, or a forged handle is invalid_params and reaches nothing.
func TestMCPMedia_HandleBindsTokenAndRow(t *testing.T) {
	stub := &mediaStub{}
	addr := mcpHTTPServer(t, stub.dispatcher(&recordingAudit{}), map[string]rest.Scope{
		"alice": rest.ScopeReadStr, "bob": rest.ScopeReadStr,
	})
	alice, bob := connectMCP(t, addr, "alice"), connectMCP(t, addr, "bob")
	handle := listHandle(t, alice)
	stub.take()
	for label, try := range map[string]struct {
		cs *mcp.ClientSession
		h  string
	}{
		"other token": {bob, handle}, "raw id": {alice, mediaMsg}, "forged": {alice, handle[:len(handle)-2] + "xx"},
	} {
		res, out := getMedia(t, try.cs, try.h)
		if !res.IsError || !strings.Contains(out, `"reason":"invalid_params"`) {
			t.Errorf("%s: IsError=%v %s", label, res.IsError, out)
		}
	}
	if got := stub.take(); len(got) != 0 {
		t.Fatalf("refused handles reached the daemon: %v", got)
	}
	res, _ := getMedia(t, alice, handle)
	er, _ := res.Content[0].(*mcp.EmbeddedResource)
	if res.IsError || er == nil || string(er.Resource.Blob) != "\x01\x02\x03" {
		t.Fatalf("own handle: %+v", res)
	}
	calls := stub.take()
	want := `media.download {"chat":"` + mediaChat + `","messageId":"` + mediaMsg + `","origin":"mcp"}`
	if len(calls) != 2 || calls[0] != want || !strings.HasPrefix(calls[1], `media.fetchBytes {"length":3,"offset":0,"sha256":"ab12"}`) {
		t.Errorf("daemon calls %q, want the listed row then its bytes", calls)
	}
}

// TestMCPMedia_Audited: each MCP list and fetch leaves a media_read record
// with source "mcp"; non-MCP callers are untouched; an audit failure
// withholds the result.
func TestMCPMedia_Audited(t *testing.T) {
	stub, audit := &mediaStub{}, &recordingAudit{}
	cs := mcpHTTPSession(t, stub.dispatcher(audit), rest.ScopeReadStr)
	if res, _ := getMedia(t, cs, listHandle(t, cs)); res.IsError {
		t.Fatalf("fetch: %+v", res)
	}
	events := audit.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit records %+v, want list + fetch", events)
	}
	for i, wantDetail := range []string{"method=media.list", "method=media.download messageId=" + mediaMsg} {
		e := events[i]
		if e.Source != "mcp" || e.Action != domain.AuditMediaRead || e.Decision != "ok" || e.Detail != wantDetail || e.Subject != domain.MustJID(mediaChat) {
			t.Errorf("event %d = %+v, want media_read via mcp %q", i, e, wantDetail)
		}
	}
	direct := auditMCPMedia("media.list", stub.handler("media.list"), audit)
	if _, err := direct(t.Context(), json.RawMessage(`{"chat":"`+mediaChat+`"}`)); err != nil || len(audit.snapshot()) != 2 {
		t.Errorf("a non-MCP call was audited as mcp (err %v)", err)
	}
	failing := auditMCPMedia("media.list", stub.handler("media.list"), failAudit{})
	if res, err := failing(t.Context(), json.RawMessage(`{"chat":"`+mediaChat+`","origin":"mcp"}`)); err == nil || res != nil {
		t.Errorf("audit failure answered %s (%v), want fail-closed", res, err)
	}
}
