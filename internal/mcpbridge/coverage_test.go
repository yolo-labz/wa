package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scriptDaemon answers each method from a script (a result, an RPC refusal
// or a transport failure) and records every call.
type scriptDaemon struct {
	mu      sync.Mutex
	calls   []recordedCall
	replies map[string]string
	refuse  map[string]*RPCError
	netErr  error
}

func (s *scriptDaemon) call(_ context.Context, method string, params any) (json.RawMessage, *RPCError, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := recordedCall{method: method}
	b, _ := json.Marshal(params)
	_ = json.Unmarshal(b, &rec.params)
	s.calls = append(s.calls, rec)
	if r, ok := s.replies[method]; ok && s.netErr == nil && s.refuse[method] == nil {
		return json.RawMessage(r), nil, nil
	}
	if s.netErr != nil || s.refuse[method] != nil {
		return nil, s.refuse[method], s.netErr
	}
	return json.RawMessage(`{}`), nil, nil
}

func (s *scriptDaemon) methods() (out []string) {
	for _, c := range s.calls {
		out = append(out, c.method)
	}
	return out
}

func (s *scriptDaemon) last(t *testing.T) recordedCall {
	t.Helper()
	return s.calls[len(s.calls)-1]
}

func adminSession(t *testing.T, d *scriptDaemon) *mcp.ClientSession {
	t.Helper()
	srv, err := NewServer(d.call, Config{Scope: ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	st, ct := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); cancel() })
	return cs
}

var notAllowlisted = &RPCError{Code: -32100, Message: "secret daemon text", Data: json.RawMessage(`{"reason":"not_allowlisted"}`)}

func TestAddUnitFields(t *testing.T) {
	const iso = "2025-11-09T01:00:00Z"
	walk := func(js string) map[string]any {
		var v map[string]any
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			t.Fatal(err)
		}
		addUnitFields(v)
		return v
	}
	v := walk(`{"ts":1762650000,"timestamp":1762650000,"lastMessageTs":1762650000,"createdAt":1762650000,"lastSeenTS":1762650000,"ATs":1762650000,"Ts":1,"tsx":1,"name":"x","fireAt":"1"}`)
	for _, base := range []string{"ts", "timestamp", "last_message", "created", "last_seen", "a"} {
		if v[base+"_unix_s"] != int64(1762650000) || v[base+"_at"] != iso {
			t.Errorf("%s: unix_s=%v at=%v", base, v[base+"_unix_s"], v[base+"_at"])
		}
	}
	if len(v) != 10+12 {
		t.Errorf("a non-time key, a bare Ts or a string time gained fields: %v", v)
	}
	// Non-positive: no _at; never overwrites; arrays and nesting are walked.
	v = walk(`{"lastMessageTs":0,"fireAt":-5,"ts":1762650000,"ts_at":"mine","ts_unix_s":7,"hits":[{"inner":{"createdAt":1762650000}}]}`)
	if v["last_message_unix_s"] != int64(0) || v["fire_unix_s"] != int64(-5) || v["last_message_at"] != nil || v["fire_at"] != nil {
		t.Errorf("non-positive times: %v", v)
	}
	if v["ts_at"] != "mine" || v["ts_unix_s"] != float64(7) {
		t.Errorf("overwrote %v", v)
	}
	if v["hits"].([]any)[0].(map[string]any)["inner"].(map[string]any)["created_at"] != iso {
		t.Errorf("not walked: %v", v)
	}
}

func TestAddUnitFields_CaptionEnvelope(t *testing.T) {
	const env = `<channel source="wa" chat="c" sender="s"><field name="body">hi</field><field name="caption">the photo</field></channel>`
	msg := func(js string) map[string]any {
		var v map[string]any
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			t.Fatal(err)
		}
		addUnitFields(v)
		return v
	}
	v := msg(`{"ts":1,"channel":` + jsonText(env) + `}`)
	if v["has_caption"] != true || v["channel"] != env ||
		v["caption"] != `<channel source="wa" chat="c" sender="s"><field name="caption">the photo</field></channel>` {
		t.Errorf("caption not cut into its own envelope: %v", v)
	}
	if v := msg(`{"ts":1,"has_caption":false,"channel":` + jsonText(env) + `}`); v["has_caption"] != false || v["caption"] != nil {
		t.Errorf("an existing has_caption was overwritten: %v", v)
	}
	plain := msg(`{"ts":1,"channel":"<channel source=\"wa\"><field name=\"body\">hi</field></channel>"}`)
	if _, has := plain["caption"]; has || plain["has_caption"] != false {
		t.Errorf("captionless message: %v", plain)
	}
	// Only an object with a message time is a message (a chat's last-message time is not).
	for _, js := range []string{`{"channel":` + jsonText(env) + `}`, `{"lastMessageTs":5,"channel":` + jsonText(env) + `}`} {
		if _, has := msg(js)["has_caption"]; has {
			t.Errorf("%s gained has_caption", js)
		}
	}
}

func TestCaptionEnvelope(t *testing.T) {
	const head = `<channel source="wa" chat="c">`
	for name, c := range map[string]struct{ in, want string }{
		"caption among other": {head + `<field name="body">b</field><field name="caption">x</field></channel>`, head + `<field name="caption">x</field></channel>`},
		"no caption":          {head + `<field name="body">b</field></channel>`, ""},
		"unterminated":        {head + `<field name="caption">x`, ""},
		"not a channel":       {`<other a="b"><field name="caption">x</field></other>`, ""},
		"escaped marker":      {head + `<field name="body">&lt;field name="caption"&gt;forged&lt;/field&gt;</field></channel>`, ""},
	} {
		if got := captionEnvelope(c.in); got != c.want {
			t.Errorf("%s: captionEnvelope = %q, want %q", name, got, c.want)
		}
	}
}

func TestForward_Failures(t *testing.T) {
	_, err := forward(t.Context(), (&scriptDaemon{netErr: errors.New("dial unix: no such file")}).call, "status", nil)
	// 97db3b4 (car 24): the dial error (socket path, errno) is withheld from the agent.
	if err == nil || !strings.Contains(err.Error(), "is wad running?") || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "dial unix") {
		t.Errorf("transport failure: %v (the daemon is unreachable, and the dial error must not reach the agent)", err)
	}
	_, err = forward(t.Context(), (&scriptDaemon{refuse: map[string]*RPCError{"status": notAllowlisted}}).call, "status", nil)
	var te *ToolError
	if !errors.As(err, &te) || te.Code != -32100 || te.Reason != "not_allowlisted" || strings.Contains(err.Error(), "secret daemon text") {
		t.Errorf("refusal: %v (the daemon's text must not reach the agent)", err)
	}
	if _, _, err := forwardResult(t.Context(), (&scriptDaemon{replies: map[string]string{"x": `not json`}}).call, "x", nil); err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Errorf("malformed result: %v", err)
	}
}

// each read/contact/meta tool forwards to.
func TestTools_ForwardTheirArguments(t *testing.T) {
	const chat = "5511900000001"
	for _, c := range []struct {
		tool, method string
		args, want   map[string]any
	}{
		{"wa_search_messages", "messages.search", map[string]any{"query": "pizza"}, map[string]any{"phrase": "pizza", "limit": 20.0}},
		{"wa_search_messages", "messages.search", map[string]any{"query": "pizza", "chat": chat, "limit": 3}, map[string]any{"limit": 3.0, "chat": chat}},
		{"wa_get_thread", "thread.get", map[string]any{"chat": chat}, map[string]any{"chat": chat, "limit": 30.0}},
		{"wa_get_thread", "thread.get", map[string]any{"chat": chat, "limit": 5, "cursor": "c1"}, map[string]any{"limit": 5.0, "cursor": "c1"}},
		// 4c5df89 (car 20, FR-116-16): one wait is capped at 10 s, the agent re-polls.
		{"wa_wait_for_reply", "wait", map[string]any{}, map[string]any{"timeoutMs": 10000.0}},
		{"wa_wait_for_reply", "wait", map[string]any{"timeout_sec": 3}, map[string]any{"timeoutMs": 3000.0}},
		{"wa_wait_for_reply", "wait", map[string]any{"timeout_sec": 400}, map[string]any{"timeoutMs": 10000.0}},
		{"wa_resolve_contact", "contacts.search", map[string]any{"query": "ana"}, map[string]any{"query": "ana", "limit": 5.0}},
		{"wa_list_chats", "chat.list", map[string]any{}, map[string]any{"limit": 20.0}},
		{"wa_draft_review", "draft.list", map[string]any{}, map[string]any{"limit": 20.0}},
		{"wa_draft_review", "draft.get", map[string]any{"draft_id": "d-1"}, map[string]any{"id": "d-1"}},
		{"wa_group_info", "groups.get", map[string]any{"jid": "1@g.us"}, map[string]any{"jid": "1@g.us"}},
		{"wa_group_info", "groups", map[string]any{}, nil},
		{"wa_status", "status", map[string]any{}, nil},
		{"wa_allowlist_check", "allow.check", map[string]any{"recipient": chat, "action": "send"}, map[string]any{"jid": chat, "action": "send"}},
	} {
		d := &scriptDaemon{}
		if res, _ := callTool(t, adminSession(t, d), c.tool, c.args); res.IsError {
			t.Errorf("%s %v: refused: %+v", c.tool, c.args, res.Content)
			continue
		}
		got := d.last(t)
		if got.method != c.method || (c.want == nil && got.params != nil) {
			t.Errorf("%s %v forwarded %q %v, want %q", c.tool, c.args, got.method, got.params, c.method)
		}
		for k, want := range c.want {
			if got.params[k] != want {
				t.Errorf("%s %v: param %s = %v, want %v", c.tool, c.args, k, got.params[k], want)
			}
		}
		d = &scriptDaemon{refuse: map[string]*RPCError{c.method: notAllowlisted}}
		res, out := callTool(t, adminSession(t, d), c.tool, c.args)
		if !res.IsError || out["reason"] != "not_allowlisted" || out["code"] != -32100.0 || strings.Contains(jsonText(res.Content), "secret daemon text") {
			t.Errorf("%s refused by the daemon: IsError=%v %v %s", c.tool, res.IsError, out, jsonText(res.Content))
		}
	}
}

func TestAllowlistCheck_RecipientResolution(t *testing.T) {
	const jid, other = "5511900000001@s.whatsapp.net", "5511900000002@s.whatsapp.net"
	hits := func(js ...string) string {
		rows := make([]string, 0, len(js))
		for _, j := range js {
			rows = append(rows, `{"jid":"`+j+`"}`)
		}
		return `{"contacts":[` + strings.Join(rows, ",") + `]}`
	}
	check := func(d *scriptDaemon, recipient string) (*mcp.CallToolResult, map[string]any) {
		return callTool(t, adminSession(t, d), "wa_allowlist_check", map[string]any{"recipient": recipient, "action": "read"})
	}
	// A JID passes through unresolved; a name resolves to its one contact.
	for _, c := range []struct{ recipient, search, calls string }{
		{jid, "", "allow.check"}, {"Ana Souza", hits(jid), "contacts.search,allow.check"},
	} {
		d := &scriptDaemon{replies: map[string]string{"contacts.search": c.search, "allow.check": `{"allowed":true}`}}
		res, out := check(d, c.recipient)
		if p := d.last(t).params; res.IsError || out["allowed"] != true || strings.Join(d.methods(), ",") != c.calls || p["jid"] != jid || p["action"] != "read" {
			t.Errorf("%q: %v %v %v", c.recipient, out, d.methods(), p)
		}
	}
	// Anything but exactly one match, or a bad or refused search, never reaches allow.check.
	for name, d := range map[string]*scriptDaemon{
		"no match":    {replies: map[string]string{"contacts.search": hits()}},
		"two matches": {replies: map[string]string{"contacts.search": hits(jid, other)}},
		"malformed":   {replies: map[string]string{"contacts.search": `[1]`}},
		"refused":     {refuse: map[string]*RPCError{"contacts.search": notAllowlisted}},
	} {
		res, out := check(d, "Ana")
		if !res.IsError || strings.Contains(strings.Join(d.methods(), ","), "allow.check") {
			t.Errorf("%s: IsError=%v methods=%v", name, res.IsError, d.methods())
		}
		if txt := jsonText(res.Content); (name == "malformed" && !strings.Contains(txt, "malformed JSON")) ||
			(strings.Contains(name, "match") && !strings.Contains(txt, "exactly one contact")) ||
			(name == "refused" && out["reason"] != "not_allowlisted") {
			t.Errorf("%s: %s", name, txt)
		}
	}
}

func TestStatusTool_AddsTimesAndAuth(t *testing.T) {
	status := func(reply, tool string) (*mcp.CallToolResult, map[string]any) {
		return callTool(t, adminSession(t, &scriptDaemon{replies: map[string]string{"status": reply}}), "wa_status", map[string]any{"tool": tool})
	}
	_, out := status(`{"connected":true,"connectedAt":1762650000,"health":"ok"}`, "wa_send_message")
	if auth, _ := out["auth"].(map[string]any); auth["scope"] != ScopeAdmin || auth["tool"] != "wa_send_message" || auth["required_scope"] != ScopeSend {
		t.Errorf("auth block %v", out["auth"])
	}
	if out["connected"] != true || out["health"] != "ok" || out["connected_unix_s"] != 1762650000.0 || out["connected_at"] != "2025-11-09T01:00:00Z" {
		t.Errorf("daemon fields or the time pair missing: %v", out)
	}
	_, out = status(`null`, "") // a null answer still yields the auth block, with no required scope
	if a, ok := out["auth"].(map[string]any); !ok || a["tool"] != nil || a["required_scope"] != nil {
		t.Errorf("null status: %v", out)
	}
	if res, _ := status(`[`, ""); !res.IsError || !strings.Contains(jsonText(res.Content), "malformed JSON") {
		t.Errorf("malformed status: IsError=%v", res.IsError)
	}
}

func TestMediaPrincipal(t *testing.T) {
	req := func(auth string) *mcp.CallToolRequest {
		r := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: http.Header{}}}
		if auth != "" {
			r.Extra.Header.Set("Authorization", auth)
		}
		return r
	}
	if mediaPrincipal(nil) != "stdio" || mediaPrincipal(&mcp.CallToolRequest{}) != "stdio" || mediaPrincipal(req("")) != "stdio" {
		t.Error("no credential means stdio")
	}
	a, b := mediaPrincipal(req("Bearer one")), mediaPrincipal(req("Bearer two"))
	if a == "stdio" || a == b || len(a) != 64 || strings.Contains(a, "one") || a != mediaPrincipal(req("Bearer one")) {
		t.Errorf("principal must be a stable per-credential digest: %q %q", a, b)
	}
}

func TestDecodeFields(t *testing.T) {
	good := encodeFields("ab", "", "xyz")
	if f, ok := decodeFields(good); !ok || strings.Join(f, "|") != "ab||xyz" {
		t.Errorf("round trip: %v", f)
	}
	for name, b := range map[string][]byte{"truncated length": good[:2], "length overruns": {0, 0, 0, 9, 'a'}, "trailing byte": append(bytes.Clone(good), 1)} {
		if f, ok := decodeFields(b); ok {
			t.Errorf("%s accepted: %v", name, f)
		}
	}
}

func TestListMedia_RowViews(t *testing.T) {
	const env = `<channel source="wa" chat="c" sender="s"><field name="caption">hi</field></channel>`
	rows := `[{"messageId":"3EB0A","chatJid":"c1","timestamp":1762650000,"mediaType":"audio","size":10,"durationSeconds":7,"channel":` + jsonText(env) + `},` +
		`{"messageId":"3EB0B","chatJid":"c1","fromMe":true,"caption":"mine","cached":true}]`
	d := &scriptDaemon{replies: map[string]string{"media.list": `{"media":` + rows + `,"appliedFilters":["chat"]}`}}
	_, out := callTool(t, adminSession(t, d), "wa_list_media", map[string]any{"chat": "c1", "sender": "s1", "media_type": "audio", "since": 5, "until": 9, "limit": 500})
	if p := d.last(t).params; p["origin"] != "mcp" || p["limit"] != 50.0 || p["mediaType"] != "audio" || p["since"] != 5.0 || p["until"] != 9.0 {
		t.Errorf("daemon params %v (limit clamps to 50, origin is mcp)", p)
	}
	rs, _ := out["media"].([]any)
	if len(rs) != 2 || jsonText(out["applied_filters"]) != `["chat"]` {
		t.Fatalf("result %v", out)
	}
	in, mine := rs[0].(map[string]any), rs[1].(map[string]any)
	if in["duration_s"] != 7.0 || in["from_me"] != false || in["size_bytes"] != 10.0 || in["media_type"] != "audio" || in["caption"] != env {
		t.Errorf("inbound row %v", in)
	}
	if _, has := mine["duration_s"]; has || mine["cached"] != true || mine["from_me"] != true || mine["channel"] != nil {
		t.Errorf("own row %v", mine)
	}
	for _, r := range []map[string]any{in, mine} {
		if h, _ := r["media_handle"].(string); h == "" || strings.Contains(jsonText(r), "3EB0") {
			t.Errorf("row exposes a raw message id or lacks a handle: %v", r)
		}
	}
}

func TestFetchMediaBytes(t *testing.T) {
	chunky := func(chunks ...string) Caller {
		i := 0
		return func(context.Context, string, any) (json.RawMessage, *RPCError, error) {
			i++
			return json.RawMessage(chunks[min(i, len(chunks))-1]), nil, nil
		}
	}
	for name, c := range map[string]struct {
		call Caller
		size int64
		want []byte
	}{
		"chunks concatenate, eof ends": {chunky(`{"bytes":"AQI="}`, `{"bytes":"AwQ=","eof":true}`), 100, []byte{1, 2, 3, 4}},
		"an empty chunk ends the loop": {chunky(`{"bytes":"AQ=="}`, `{"bytes":""}`), 100, []byte{1}},
		"the declared size bounds":     {chunky(`{"bytes":"AQID"}`), 3, []byte{1, 2, 3}},
	} {
		if got, err := fetchMediaBytes(t.Context(), c.call, "sha", c.size); err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	if _, err := fetchMediaBytes(t.Context(), chunky(`{"bytes":`), "sha", 3); err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Errorf("malformed chunk: %v", err)
	}
}

// TestMedia_DaemonAnswersThatAreWrong: a malformed answer is an error the
// agent can read, and a refused byte fetch is a typed refusal.
func TestMedia_DaemonAnswersThatAreWrong(t *testing.T) {
	h := mintMediaHandle("stdio", "5511900000001", "3EB0A", time.Now().Add(time.Minute))
	for _, c := range []struct {
		tool, method string
		args         map[string]any
	}{
		{"wa_list_media", "media.list", map[string]any{"chat": "c", "sender": "s"}},
		{"wa_get_media", "media.download", map[string]any{"media_handle": h}},
		{"wa_transcribe_voice", "media.download", map[string]any{"media_handle": h}},
	} {
		res, _ := callTool(t, adminSession(t, &scriptDaemon{replies: map[string]string{c.method: `[`}}), c.tool, c.args)
		if !res.IsError || !strings.Contains(jsonText(res.Content), "malformed JSON") {
			t.Errorf("%s: IsError=%v %s", c.tool, res.IsError, jsonText(res.Content))
		}
		res, out := callTool(t, adminSession(t, &scriptDaemon{refuse: map[string]*RPCError{c.method: notAllowlisted}}), c.tool, c.args)
		if !res.IsError || out["reason"] != "not_allowlisted" {
			t.Errorf("%s refused: IsError=%v %v", c.tool, res.IsError, out)
		}
	}
	d := &scriptDaemon{
		replies: map[string]string{"media.download": `{"object":{"sha256":"ab","mime":"image/png","size":4}}`},
		refuse:  map[string]*RPCError{"media.fetchBytes": notAllowlisted},
	}
	if res, out := callTool(t, adminSession(t, d), "wa_get_media", map[string]any{"media_handle": h}); !res.IsError || out["reason"] != "not_allowlisted" {
		t.Errorf("refused fetch: IsError=%v %v", res.IsError, out)
	}
}

func TestTranscribeVoice(t *testing.T) {
	h := mintMediaHandle("stdio", "5511900000001", "3EB0A", time.Now().Add(time.Minute))
	const env = `<channel source="wa" chat="c" sender="s">olá</channel>`
	d := &scriptDaemon{replies: map[string]string{"media.download": `{"object":{"mime":"audio/ogg","durationSeconds":9,"channel":` + jsonText(env) + `,"path":"/srv/secret.ogg","sha256":"ab"}}`}}
	res, out := callTool(t, adminSession(t, d), "wa_transcribe_voice", map[string]any{"media_handle": h})
	if res.IsError || out["transcript"] != env || out["mime"] != "audio/ogg" || out["duration_s"] != 9.0 || len(out) != 3 {
		t.Errorf("transcript result %v: want only transcript, mime, duration_s", out)
	}
	if p := d.last(t).params; p["transcribe"] != true || p["origin"] != "mcp" || p["chat"] != "5511900000001" || p["messageId"] != "3EB0A" {
		t.Errorf("daemon params %v", p)
	}
	for _, tool := range []string{"wa_transcribe_voice", "wa_get_media"} {
		if res, out := callTool(t, adminSession(t, d), tool, map[string]any{"media_handle": "x"}); !res.IsError || out["reason"] != "invalid_params" {
			t.Errorf("%s with an unknown handle: %v", tool, out)
		}
	}
}
