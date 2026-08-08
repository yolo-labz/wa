// Package main — end-to-end tests for the #173/#180 file-management CLI
// surface: `wa chat list`/`last-active`, `wa contacts list`, `wa messages
// list` (filters + tri-state --from-me + RFC3339 parsing), `wa media list`,
// `wa media gc --dry-run` NDJSON, and the new `wa export` time/limit filters.
//
// Reuses the fakeDaemon (cmd_t121_e2e_test.go) + runCmd (cmd_profile_e2e_test.go)
// harness: a throwaway unix-socket JSON-RPC server, in-process cobra invocation.
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func decodeParams(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode params: %v", err)
	}
}

// TestWaChatList covers `wa chat list`: the default limit is forwarded and
// the human table renders a header + row.
func TestWaChatList(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("chat.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"chats": []any{
			map[string]any{
				"jid":           "5581998398677@s.whatsapp.net",
				"pushName":      "Alice",
				"lastMessageTs": 1700000000,
				"messageCount":  42,
				"isGroup":       false,
			},
		}}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "chat", "list")

	if !strings.Contains(stdout, "LAST") || !strings.Contains(stdout, "MSGS") {
		t.Fatalf("human table header missing:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, "Alice") || !strings.Contains(stdout, "dm") {
		t.Fatalf("human table row missing:\nstdout=%q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "chat.list" {
		t.Fatalf("expected 1 chat.list call, got %+v", calls)
	}
	var p struct {
		Limit int `json:"limit"`
	}
	decodeParams(t, calls[0].Params, &p)
	if p.Limit != 200 {
		t.Fatalf("chat list default limit = %d, want 200", p.Limit)
	}
}

// TestWaChatListJSON covers `wa chat list --json`: each chat is a schema-tagged
// NDJSON line.
func TestWaChatListJSON(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("chat.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"chats": []any{
			map[string]any{"jid": "g1@g.us", "isGroup": true, "messageCount": 3},
		}}, nil
	})

	stdout, _ := runCmd(t, "--socket", fd.path(), "--json", "chat", "list")

	if !strings.Contains(stdout, `"schema":"wa.chat.list/v1"`) {
		t.Fatalf("missing NDJSON schema envelope:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"jid":"g1@g.us"`) {
		t.Fatalf("missing chat row:\n%s", stdout)
	}
}

// TestWaChatLastActive confirms `last-active` hits the same RPC with the
// smaller default limit of 10.
func TestWaChatLastActive(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("chat.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"chats": []any{}}, nil
	})

	stdout, _ := runCmd(t, "--socket", fd.path(), "chat", "last-active")

	if !strings.Contains(stdout, "No chats") {
		t.Fatalf("empty chat.list should print 'No chats', got %q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "chat.list" {
		t.Fatalf("expected 1 chat.list call, got %+v", calls)
	}
	var p struct {
		Limit int `json:"limit"`
	}
	decodeParams(t, calls[0].Params, &p)
	if p.Limit != 10 {
		t.Fatalf("last-active default limit = %d, want 10", p.Limit)
	}
}

// TestWaContactsList covers `wa contacts list`: default limit 100, response
// echoed.
func TestWaContactsList(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("contacts.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"contacts": []any{
			map[string]any{"jid": "5581998398677@s.whatsapp.net", "displayName": "Alice"},
		}}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "--json", "contacts", "list")

	if !strings.Contains(stdout, `"schema":"wa.contacts.list/v1"`) {
		t.Fatalf("missing schema envelope:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, "Alice") {
		t.Fatalf("missing contact:\n%s", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "contacts.list" {
		t.Fatalf("expected 1 contacts.list call, got %+v", calls)
	}
	var p struct {
		Limit int `json:"limit"`
	}
	decodeParams(t, calls[0].Params, &p)
	if p.Limit != 100 {
		t.Fatalf("contacts list default limit = %d, want 100", p.Limit)
	}
}

// TestWaMessagesListFilters threads every filter and asserts the on-wire
// params, including the tri-state --from-me=true and an RFC3339 --since that
// converts to unix seconds.
func TestWaMessagesListFilters(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("messages.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{}}, nil
	})

	since := "2026-01-01T00:00:00Z"
	wantSince, _ := time.Parse(time.RFC3339, since)

	_, _ = runCmd(t, "--socket", fd.path(),
		"messages", "list",
		"--chat", "5581998398677@s.whatsapp.net",
		"--media-type", "audio",
		"--from-me",
		"--since", since,
		"--limit", "20",
	)

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "messages.list" {
		t.Fatalf("expected 1 messages.list call, got %+v", calls)
	}
	var p struct {
		Chat      string `json:"chat"`
		MediaType string `json:"mediaType"`
		FromMe    *bool  `json:"fromMe"`
		Since     int64  `json:"since"`
		Limit     int    `json:"limit"`
	}
	decodeParams(t, calls[0].Params, &p)
	if p.Chat != "5581998398677@s.whatsapp.net" {
		t.Errorf("chat not threaded: %q", p.Chat)
	}
	if p.MediaType != "audio" {
		t.Errorf("mediaType not threaded: %q", p.MediaType)
	}
	if p.FromMe == nil || !*p.FromMe {
		t.Errorf("fromMe should be true, got %v", p.FromMe)
	}
	if p.Since != wantSince.Unix() {
		t.Errorf("since = %d, want %d", p.Since, wantSince.Unix())
	}
	if p.Limit != 20 {
		t.Errorf("limit = %d, want 20", p.Limit)
	}
}

// TestWaMessagesListTriStateFromMe proves the default omits fromMe entirely
// (list both directions) while --from-me=false forwards an explicit false.
func TestWaMessagesListTriStateFromMe(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("messages.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{}}, nil
	})

	// Default: no --from-me → key absent.
	_, _ = runCmd(t, "--socket", fd.path(), "messages", "list")
	var keys map[string]json.RawMessage
	decodeParams(t, fd.seen()[0].Params, &keys)
	if _, ok := keys["fromMe"]; ok {
		t.Errorf("default messages list must omit fromMe, params=%s", string(fd.seen()[0].Params))
	}

	// Explicit --from-me=false → fromMe present and false.
	_, _ = runCmd(t, "--socket", fd.path(), "messages", "list", "--from-me=false")
	var p struct {
		FromMe *bool `json:"fromMe"`
	}
	last := fd.seen()
	decodeParams(t, last[len(last)-1].Params, &p)
	if p.FromMe == nil || *p.FromMe {
		t.Errorf("--from-me=false should forward fromMe:false, got %v", p.FromMe)
	}
}

// TestWaMessagesListBadSince asserts a non-RFC3339 --since exits non-zero with
// an explanatory message (shared parseTimeFlag).
func TestWaMessagesListBadSince(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("messages.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{}}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(),
		"messages", "list", "--since", "yesterday")

	if !strings.Contains(stderr, "exec error") {
		t.Fatalf("bad --since should exit non-zero, stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "RFC3339") {
		t.Fatalf("bad --since should explain RFC3339, stderr=%q", stderr)
	}
	if calls := fd.seen(); len(calls) != 0 {
		t.Fatalf("no RPC should fire on a parse error, got %+v", calls)
	}
}

// TestWaMediaList covers `wa media list --json`: filters threaded, NDJSON
// schema lines emitted.
func TestWaMediaList(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{
			// A filtered call must confirm what it applied, or the client
			// refuses the rows — see TestWaMediaListRefusesUnconfirmedFilters.
			"appliedFilters": []string{"chat", "mediaType"},
			"media": []any{
				map[string]any{
					"messageId":       "msg_01",
					"chatJid":         "5581998398677@s.whatsapp.net",
					"timestamp":       1700000000,
					"mediaType":       "audio/ogg",
					"sha256":          "deadbeefdeadbeef",
					"size":            2048,
					"durationSeconds": 7,
					"cached":          true,
				},
			},
		}, nil
	})

	stdout, _ := runCmd(t, "--socket", fd.path(), "--json",
		"media", "list", "--chat", "5581998398677@s.whatsapp.net", "--media-type", "audio")

	if !strings.Contains(stdout, `"schema":"wa.media.list/v1"`) {
		t.Fatalf("missing NDJSON schema envelope:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"messageId":"msg_01"`) {
		t.Fatalf("missing media row:\n%s", stdout)
	}
	calls := fd.seen()
	var p struct {
		Chat      string `json:"chat"`
		MediaType string `json:"mediaType"`
		Limit     int    `json:"limit"`
	}
	decodeParams(t, calls[0].Params, &p)
	if p.Chat == "" || p.MediaType != "audio" || p.Limit != 50 {
		t.Fatalf("media list params wrong: %+v", p)
	}
}

// TestWaMediaListFromMeTriState pins the WAA-14 CLI tri-state: --from-me
// serializes fromMe:true, --not-from-me serializes fromMe:false, and the
// two flags together are refused (mutually exclusive). The param reaching
// the daemon is the wire contract an audio poller depends on to tell its
// own voice notes from a reply.
func TestWaMediaListFromMeTriState(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want *bool
		err  bool
	}{
		{"from-me", []string{"--from-me"}, boolPtrForTest(true), false},
		{"not-from-me", []string{"--not-from-me"}, boolPtrForTest(false), false},
		{"neither", nil, nil, false},
		{"both-refused", []string{"--from-me", "--not-from-me"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := newFakeDaemon(t)
			fd.on("media.list", func(json.RawMessage) (any, *rpcError) {
				return map[string]any{
					"appliedFilters": []string{"fromMe"},
					"media":          []any{},
				}, nil
			})

			args := []string{"--socket", fd.path(), "--json", "media", "list"}
			args = append(args, tc.args...)
			_, stderr := runCmd(t, args...)
			if tc.err {
				if !strings.Contains(stderr, "mutually exclusive") {
					t.Fatalf("both flags together must be refused, stderr=%q", stderr)
				}
				return
			}
			if strings.Contains(stderr, "[exec error") {
				t.Fatalf("runCmd failed: %q", stderr)
			}
			calls := fd.seen()
			if len(calls) != 1 {
				t.Fatalf("got %d media.list calls, want 1", len(calls))
			}
			var p struct {
				FromMe *bool `json:"fromMe"`
			}
			decodeParams(t, calls[0].Params, &p)
			if (p.FromMe == nil) != (tc.want == nil) {
				t.Fatalf("fromMe nil-ness = %v, want %v", p.FromMe == nil, tc.want == nil)
			}
			if tc.want != nil && (p.FromMe == nil || *p.FromMe != *tc.want) {
				t.Fatalf("fromMe = %v, want %v", p.FromMe, *tc.want)
			}
		})
	}
}

func boolPtrForTest(b bool) *bool { return &b }

// TestWaMediaListHumanTable checks the human renderer prints the header and a
// human-readable size + cached marker.
func TestWaMediaListHumanTable(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"media": []any{
			map[string]any{"messageId": "m", "timestamp": 1700000000, "mediaType": "image/jpeg", "size": 2048, "cached": true, "sha256": "abcdef0123456789"},
		}}, nil
	})

	stdout, _ := runCmd(t, "--socket", fd.path(), "media", "list")

	for _, want := range []string{"TIME", "TYPE", "SIZE", "CACHED", "SHA256", "2.0KB", "yes"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("media table missing %q:\n%s", want, stdout)
		}
	}
}

// TestWaMediaGCDryRunNDJSON covers #180: a human-mode dry run streams candidate
// files as NDJSON on stdout and a reclaimable summary on stderr.
func TestWaMediaGCDryRunNDJSON(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.gc", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			DryRun bool `json:"dryRun"`
		}
		_ = json.Unmarshal(params, &p)
		if !p.DryRun {
			return nil, &rpcError{Code: -32602, Message: "expected dryRun"}
		}
		return map[string]any{
			"candidates": 2,
			"deleted":    0,
			"bytesFreed": 0,
			"dryRun":     true,
			"files": []any{
				map[string]any{"sha256": "aaa", "path": "/cache/aa/a.ogg", "size": 1024, "ageSeconds": 100},
				map[string]any{"sha256": "bbb", "path": "/cache/bb/b.ogg", "size": 1024, "ageSeconds": 200},
			},
		}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "media", "gc", "--dry-run")

	if !strings.Contains(stdout, `"schema":"wa.media.gc/v1"`) {
		t.Fatalf("dry-run stdout missing NDJSON schema:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"path":"/cache/aa/a.ogg"`) {
		t.Fatalf("dry-run stdout missing candidate file:\n%s", stdout)
	}
	if !strings.Contains(stderr, "dry-run: 2 candidate(s)") {
		t.Fatalf("dry-run stderr missing summary:\n%s", stderr)
	}
	if !strings.Contains(stderr, "reclaimable") {
		t.Fatalf("dry-run stderr missing reclaimable total:\n%s", stderr)
	}
}

// TestWaMediaGCRealCompact confirms a non-dry-run keeps the compact
// formatResult output (no per-file NDJSON).
func TestWaMediaGCRealCompact(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.gc", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"candidates": 5, "deleted": 5, "bytesFreed": 4096, "dryRun": false}, nil
	})

	stdout, _ := runCmd(t, "--socket", fd.path(), "media", "gc")

	if strings.Contains(stdout, `"schema":"wa.media.gc/v1"`) {
		t.Fatalf("real gc must not stream NDJSON files:\n%s", stdout)
	}
	if !strings.Contains(stdout, "candidates") || !strings.Contains(stdout, "deleted") {
		t.Fatalf("real gc summary missing counts:\n%s", stdout)
	}
}

// TestWaExportFilters covers the new --since/--until/--limit flags: they are
// parsed and threaded into the export params.
func TestWaExportFilters(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("export", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{
			map[string]any{"messageId": "msg_01", "timestamp": 1700000000, "body": "hi"},
		}}, nil
	})

	since := "2026-01-01T00:00:00Z"
	until := "2026-02-01T00:00:00Z"
	wantSince, _ := time.Parse(time.RFC3339, since)
	wantUntil, _ := time.Parse(time.RFC3339, until)

	stdout, stderr := runCmd(t, "--socket", fd.path(),
		"export", "--chat", "5581998398677@s.whatsapp.net",
		"--since", since, "--until", until, "--limit", "5")

	if strings.Contains(stderr, "exec error") {
		t.Fatalf("filtered export should exit 0, stderr=%q", stderr)
	}
	if !strings.Contains(stdout, `"messageId":"msg_01"`) {
		t.Fatalf("export stdout missing message:\n%s", stdout)
	}
	calls := fd.seen()
	var p struct {
		Chat  string `json:"chat"`
		Since int64  `json:"since"`
		Until int64  `json:"until"`
		Limit int    `json:"limit"`
	}
	decodeParams(t, calls[0].Params, &p)
	if p.Since != wantSince.Unix() || p.Until != wantUntil.Unix() {
		t.Errorf("time bounds wrong: since=%d until=%d", p.Since, p.Until)
	}
	if p.Limit != 5 {
		t.Errorf("limit = %d, want 5", p.Limit)
	}
}
