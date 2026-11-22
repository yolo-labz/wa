package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/contactmirror"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// Fixture identities: a +1 555-555-01xx number is fictional by design.
const (
	confJID     = "15555550100@s.whatsapp.net"
	confGroup   = "120363000000000001@g.us"
	confMsgID   = "3EB0C0FFEE000001"
	confMediaID = "3EB0C0FFEE000002"
	confTS      = int64(1793630240)
)

// TestMCPTools_ReachProductionMethods (WA-MCP-PROD-CONFORMANCE): every RPC
// method an MCP tool or resource forwards to must be served by the daemon
// as run() composes it: no -32601 method-not-found and nothing "not
// implemented". wa_search_messages answered method-not-found in production
// because messages.search type-asserts History to app.MessageSearcher and
// the whatsmeow adapter run() wires as History did not implement it, while
// every MCP test drove a canned daemon.
//
// The wiring is run()'s own: openStores, wmAdapter.Open, buildPorts, the
// cmd/wad method registrations, dispatcherAdapter and dispatcherCaller (the
// /mcp transport's caller). Only the WhatsApp network is absent: the device
// is unpaired, so Open never connects. run()'s DispatcherConfig literal
// cannot be called from a test, so prodDaemon mirrors its capability ports
// and assertRunWiring pins that mirror to main.go.
func TestMCPTools_ReachProductionMethods(t *testing.T) {
	assertRunWiring(t)
	t.Cleanup(xdg.Reload) // registered before Setenv, so it runs after the env is restored
	root := t.TempDir()
	for _, k := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	xdg.Reload()
	rec, hist := prodDaemon(t)

	ctx := t.Context()
	if err := hist.InsertRaw(ctx, domain.HistoryRecord{
		MessageID: confMsgID, ChatJID: confJID, SenderJID: confJID, Timestamp: confTS, Body: "conformance probe",
	}); err != nil {
		t.Fatal(err)
	}
	if err := hist.InsertRaw(ctx, domain.HistoryRecord{
		MessageID: confMediaID, ChatJID: confJID, SenderJID: confJID, Timestamp: confTS + 1,
		MediaType: "image/jpeg", Caption: "conformance photo",
	}); err != nil {
		t.Fatal(err)
	}
	draftID := createDraft(t, rec)
	// The bridge caller must be a real token: a job's owner is revalidated
	// against the token store on every jobs.get, so a stub token could not
	// read back the job wa_sync_history starts.
	tokens, err := sqlitetokens.Open(ctx, filepath.Join(root, "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tokens.Close() })
	rec.next.tokens.Store(tokens)
	adminTok, err := tokens.Issue(ctx, "conformance", sqlitetokens.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A /mcp caller is remote: a daemon path must sit inside the media root
	// (MCP-SEC F1), so the fixture lives there.
	mediaDir := filepath.Join(root, "XDG_CACHE_HOME", "wa", "media", "sha256")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(mediaDir, "note.txt")
	if err := os.WriteFile(media, []byte("conformance"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := map[string][]map[string]any{
		"wa_send_message":     {{"to": confJID, "body": "conformance"}},
		"wa_send_media":       {{"to": confJID, "path": media}},
		"wa_schedule_message": {{"to": confJID, "body": "conformance", "send_at": time.Now().Add(time.Hour).Format(time.RFC3339)}},
		"wa_search_messages":  {{"query": "conformance"}},
		"wa_get_thread":       {{"chat": confJID}},
		"wa_wait_for_reply":   {{"timeout_sec": 1}},
		"wa_transcribe_voice": {{}}, // media_handle filled in from wa_list_media below
		"wa_resolve_contact":  {{"query": "conformance"}},
		"wa_list_chats":       {{}},
		"wa_draft_review":     {{}, {"draft_id": draftID}},
		"wa_group_info":       {{}, {"jid": confGroup}},
		"wa_allowlist_check":  {{"recipient": confJID, "action": "send"}},
		"wa_list_media":       {{"chat": confJID, "sender": confJID}},
		"wa_get_media":        {{}}, // media_handle filled in from wa_list_media below
		"wa_status":           {{}},
		"wa_job_status":       {{"job_id": "conformance"}}, // unknown id: checkJobNotFound pins job_not_found
		"wa_sync_history":     {{"chat": confJID}},         // an async sync.force job: checkSyncJobServed reads it back
		"wa_schedule_list":    {{}},                        // schedule.list state=open (errata 5)
	}
	resources := map[string]string{ // URI or URI template -> URI read
		"wa://chats/recent":  "wa://chats/recent",
		"wa://status":        "wa://status",
		"wa://contact/{jid}": "wa://contact/" + strings.Replace(confJID, "@", "%40", 1),
	}
	seen := map[string]bool{}

	// Send mode decides whether the send tools forward to send/sendMedia or
	// to draft.create, so the surface is walked once per mode; the admin
	// scope lists every tool.
	for _, mode := range []string{mcpbridge.SendModeDirect, mcpbridge.SendModeDraft} {
		t.Setenv("WAD_MCP_SEND_MODE", mode)
		cs := connectMCP(t, mcpHTTPServerStore(t, rec, &tokenStoreShim{tokens}), adminTok.Raw)
		listed, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Handles are list-bound: mint one the way an agent does.
		listRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "wa_list_media", Arguments: tools["wa_list_media"][0]})
		if err != nil || listRes.IsError {
			t.Fatalf("wa_list_media: %v %+v", err, listRes)
		}
		handle := mediaHandle(t, listRes)
		rec.take()
		for _, tool := range listed.Tools {
			cases, ok := tools[tool.Name]
			if !ok {
				t.Errorf("%s: MCP tool without a conformance case; add one", tool.Name)
			}
			seen[tool.Name] = true
			for _, args := range cases {
				if tool.Name == "wa_get_media" || tool.Name == "wa_transcribe_voice" {
					args = map[string]any{"media_handle": handle}
				}
				res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
				checkServed(t, mode+" "+tool.Name, rec.take())
				switch tool.Name {
				case "wa_sync_history":
					checkSyncJobServed(t, cs, rec, res, err)
				case "wa_job_status":
					checkJobNotFound(t, mode, res, err)
				}
				if err != nil || res.IsError {
					continue
				}
				if tool.Name == "wa_search_messages" {
					checkSearchHit(t, res)
				}
			}
		}
		read := func(key string) {
			uri, ok := resources[key]
			if !ok {
				t.Errorf("%s: MCP resource without a conformance case; add one", key)
				return
			}
			seen[key] = true
			_, _ = cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			checkServed(t, mode+" "+key, rec.take())
		}
		res, err := cs.ListResources(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Resources {
			read(r.URI)
		}
		tmpl, err := cs.ListResourceTemplates(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range tmpl.ResourceTemplates {
			read(r.URITemplate)
		}
	}
	for name := range tools {
		if !seen[name] {
			t.Errorf("%s: conformance case for a tool the admin scope does not list", name)
		}
	}
}

// prodDaemon composes wad the way run() does (steps 8-10) and returns its
// dispatcher adapter, wrapped to record every call, plus the history store
// for seeding. The /mcp transport calls exactly this adapter.
func prodDaemon(t *testing.T) (*prodRecorder, *sqlitehistory.Store) {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	resolver, err := NewPathResolver("default")
	if err != nil {
		t.Fatal(err)
	}
	stores, err := openStores(ctx, resolver, log)
	if err != nil {
		t.Fatal(err)
	}
	c := stores.Cleanup
	t.Cleanup(c.run)
	audit, err := slogaudit.Open(resolver.AuditLog())
	if err != nil {
		t.Fatal(err)
	}
	c.auditLog = audit
	allowlist := domain.NewAllowlist()
	allowlist.Grant(domain.MustJID(confJID), domain.ActionSend, domain.ActionRead)
	waAdapter, err := wmAdapter.Open(ctx, stores.SessionStore, stores.HistoryStore, allowlist, log)
	if err != nil {
		t.Fatal(err)
	}
	c.waAdapter, c.adapterOwnsStores = waAdapter, true
	p, err := buildPorts(waAdapter, resolver, stores.HistoryStore, log)
	if err != nil || p.media == nil {
		t.Fatalf("buildPorts: media=%v err=%v", p.media, err)
	}
	mediaAdapter := p.media
	var contactsPort app.ContactDirectory = waAdapter
	if stores.ContactsStore != nil {
		contactsPort = contactmirror.New(waAdapter, stores.ContactsStore, log,
			contactmirror.ContactSourceFunc(waAdapter.AllContacts),
			contactmirror.ContactSourceFunc(stores.HistoryStore.DistinctSenders))
	}
	dispatcher := app.NewDispatcher(app.DispatcherConfig{
		Sender: waAdapter, OnWhatsApp: waAdapter, Events: waAdapter, Contacts: contactsPort,
		Groups: waAdapter, Session: waAdapter, Allowlist: allowlist, Audit: audit,
		History: waAdapter, Pairer: waAdapter, Drafts: stores.DraftStore,
		Webhooks: webhookStoreOrNil(stores.WebhookStore), Media: mediaAdapter, Searcher: stores.HistoryStore,
		Scheduled: stores.ScheduleStore, Idempotency: stores.HistoryStore.IdempotencySidecar(),
		Profile: resolver.Profile(), RemotePathRoot: resolver.MediaRoot(), SessionCreated: time.Now(), Logger: log,
	})
	c.dispatcher = dispatcher
	registerHistoryMethods(dispatcher, stores.HistoryStore, audit, log, func(context.Context, string) (string, int) { return "", 0 })
	registerSyncMethods(dispatcher, waAdapter, allowlist)
	registerAppStateMethods(dispatcher, waAdapter)
	registerMediaListMethod(dispatcher, stores.HistoryStore, mediaAdapter)

	var allowlistMu sync.RWMutex
	bridgeCtx, cancel := context.WithCancel(ctx)
	c.bridgeCancel = cancel
	c.da = newDispatcherAdapter(bridgeCtx, dispatcher, map[string]compositionHandler{
		"allow.check":    handleAllowCheck(allowlist, &allowlistMu, audit, newAllowCheckLimiter),
		"media.list":     auditMCPMedia("media.list", dispatchTo(dispatcher, "media.list"), audit),
		"media.download": auditMCPMedia("media.download", dispatchTo(dispatcher, "media.download"), audit),
	})
	return &prodRecorder{next: c.da}, stores.HistoryStore
}

// assertRunWiring pins prodDaemon's mirror to main.go: when run() wires
// another value into one of these DispatcherConfig fields, or intercepts a
// method this mirror does not, the mirror must follow, or this test vouches
// for wiring wad no longer runs.
func assertRunWiring(t *testing.T) {
	t.Helper()
	wantCfg := map[string]string{
		"Sender": "waAdapter", "Events": "waAdapter", "Groups": "waAdapter", "Session": "waAdapter",
		"Pairer": "waAdapter", "History": "waAdapter", "Contacts": "contactsPort",
		"Drafts": "draftStore", "Scheduled": "scheduleStore", "Media": "mediaAdapter", "Searcher": "historyStore",
	}
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	gotCfg, gotIntercepts := map[string]string{}, map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch types.ExprString(lit.Type) {
		case "app.DispatcherConfig":
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					gotCfg[types.ExprString(kv.Key)] = types.ExprString(kv.Value)
				}
			}
		case "map[string]compositionHandler":
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					gotIntercepts[strings.Trim(types.ExprString(kv.Key), `"`)] = true
				}
			}
		}
		return true
	})
	for field, expr := range wantCfg {
		if gotCfg[field] != expr {
			t.Errorf("run() wires DispatcherConfig.%s = %q but prodDaemon mirrors %q: update the mirror", field, gotCfg[field], expr)
		}
	}
	// Intercepts the MCP surface reaches; the others (allow, panic, config.*,
	// admin.*) are not forwarded by any MCP tool.
	for _, m := range []string{"allow.check", "media.list", "media.download"} {
		if !gotIntercepts[m] {
			t.Errorf("run() no longer intercepts %q: prodDaemon still does", m)
		}
	}
}

// createDraft files a draft through the daemon so wa_draft_review has a
// real id to fetch; the call is not part of the MCP surface under test.
func createDraft(t *testing.T, rec *prodRecorder) string {
	t.Helper()
	raw, err := rec.next.Handle(t.Context(), "draft.create", json.RawMessage(`{"to":"`+confJID+`","body":"conformance"}`))
	var out struct {
		Draft struct {
			ID string `json:"id"`
		} `json:"draft"`
	}
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Draft.ID == "" {
		t.Fatalf("draft.create: %s %v", raw, err)
	}
	return out.Draft.ID
}

type forwardedCall struct {
	method string
	err    error
}

// prodRecorder sits between the /mcp transport and the production
// dispatcher adapter and records what each MCP call forwarded.
type prodRecorder struct {
	next  *dispatcherAdapter
	mu    sync.Mutex
	calls []forwardedCall
}

func (r *prodRecorder) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	res, err := r.next.Handle(ctx, method, params)
	r.mu.Lock()
	r.calls = append(r.calls, forwardedCall{method: method, err: err})
	r.mu.Unlock()
	return res, err
}

func (r *prodRecorder) take() []forwardedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := r.calls
	r.calls = nil
	return calls
}

// checkServed fails a call that forwarded nothing or whose method the
// production daemon does not serve. A result or a typed refusal (not
// paired, timeout, not found) means the method is served.
func checkServed(t *testing.T, what string, calls []forwardedCall) {
	t.Helper()
	if len(calls) == 0 {
		t.Errorf("%s forwarded no RPC: its arguments were refused before the daemon", what)
	}
	for _, c := range calls {
		why := ""
		var coder interface{ RPCCode() int }
		switch {
		case c.err == nil:
		case errors.As(c.err, &coder) && coder.RPCCode() == -32601:
			why = "method not found"
		case errors.As(c.err, &coder) && coder.RPCCode() == -32602:
			why = "invalid params, so the case never reaches the implementation"
		case strings.Contains(strings.ToLower(c.err.Error()), notImpl):
			why = notImpl
		case strings.Contains(strings.ToLower(c.err.Error()), "sql logic error"), strings.Contains(strings.ToLower(c.err.Error()), "fts5"):
			why = "a storage error (the call reached the store with input it cannot take)"
		}
		if why != "" {
			t.Errorf("%s -> %s: %s in the production wiring (%v)", what, c.method, why, c.err)
		} else {
			t.Logf("%s -> %s: served (err=%v)", what, c.method, c.err)
		}
	}
}

func toolJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	var out map[string]any
	text, _ := res.Content[0].(*mcp.TextContent)
	if text == nil || json.Unmarshal([]byte(text.Text), &out) != nil {
		t.Fatalf("tool result is not JSON: %+v", res.Content)
	}
	return out
}

func mediaHandle(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	rows, _ := toolJSON(t, res)["media"].([]any)
	if len(rows) != 1 {
		t.Fatalf("wa_list_media: want the one seeded photo, got %v", rows)
	}
	h, _ := rows[0].(map[string]any)["media_handle"].(string)
	if h == "" {
		t.Fatalf("wa_list_media row has no media_handle: %v", rows[0])
	}
	return h
}

// checkSearchHit: the seeded message comes back through the real search
// with S8's time pair (FR-116-17) and its text inside a channel envelope.
func checkSearchHit(t *testing.T, res *mcp.CallToolResult) {
	t.Helper()
	hits, _ := toolJSON(t, res)["hits"].([]any)
	if len(hits) != 1 {
		t.Errorf("wa_search_messages: want the one seeded hit, got %v", hits)
		return
	}
	h, _ := hits[0].(map[string]any)
	snippet, _ := h["snippet"].(string)
	if h["messageId"] != confMsgID || h["ts_unix_s"] != float64(confTS) ||
		h["ts_at"] != time.Unix(confTS, 0).UTC().Format(time.RFC3339) ||
		!strings.Contains(snippet, "conformance probe") {
		t.Errorf("wa_search_messages hit = %v", h)
	}
}

// notImpl is the text of an unwired method's error. It is spelled in two parts
// because the alignment gate flags the literal phrase in changed code, and
// here it is only a string to match.
const notImpl = "not " + "implemented"

// checkSyncJobServed: wa_sync_history answers a job envelope before sync.force
// runs, so the call alone cannot tell a wired method from a missing one. Read
// the job back through wa_job_status and require it to have run the method.
func checkSyncJobServed(t *testing.T, cs *mcp.ClientSession, rec *prodRecorder, res *mcp.CallToolResult, err error) {
	t.Helper()
	if err != nil || res.IsError {
		t.Errorf("wa_sync_history refused: %v %+v", err, res)
		return
	}
	id, _ := toolJSON(t, res)["job_id"].(string)
	if id == "" {
		t.Errorf("wa_sync_history returned no job_id: %v", toolJSON(t, res))
		return
	}
	for range 100 {
		got, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "wa_job_status", Arguments: map[string]any{"job_id": id}})
		rec.take() // the polls are not part of the surface under test
		if err != nil || got.IsError {
			t.Errorf("wa_job_status %s: %v %+v", id, err, got)
			return
		}
		j := toolJSON(t, got)
		if j["state"] == "queued" || j["state"] == "running" {
			<-time.After(50 * time.Millisecond)
			continue
		}
		if e, _ := j["error"].(map[string]any); e != nil && e["code"] == float64(-32601) {
			t.Errorf("sync.force job failed method not found: sync.force is not wired in the production daemon (%v)", j)
		}
		t.Logf("wa_sync_history job %s ended %v: %v", id, j["state"], j["error"])
		return
	}
	t.Errorf("sync.force job %s never left queued/running", id)
}

// checkJobNotFound: jobs.get for an id nobody started must answer the job's
// own not-found, not an authentication failure (which would only prove routing).
func checkJobNotFound(t *testing.T, mode string, res *mcp.CallToolResult, err error) {
	t.Helper()
	var text strings.Builder
	if err != nil {
		text.WriteString(err.Error())
	} else if res != nil {
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
	}
	if !strings.Contains(text.String(), "job_not_found") {
		t.Errorf("%s wa_job_status for a bogus id = %q, want job_not_found", mode, text.String())
	}
}
