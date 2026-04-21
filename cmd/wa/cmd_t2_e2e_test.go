package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaReplyInlineQuote exercises `wa reply --to … --quoted-id … --body …`
// (FR-070 / T2-11). Asserts the CLI emits exactly one `send.reply` RPC with
// the three params and surfaces the daemon's messageId in --json output.
func TestWaReplyInlineQuote(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("send.reply", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			To       string `json:"to"`
			QuotedID string `json:"quotedId"`
			Body     string `json:"body"`
		}
		_ = json.Unmarshal(params, &p)
		if p.To != "5511900000000@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong to"}
		}
		if p.QuotedID != "orig-42" {
			return nil, &rpcError{Code: -32602, Message: "wrong quoted id"}
		}
		if p.Body != "ack" {
			return nil, &rpcError{Code: -32602, Message: "wrong body"}
		}
		return map[string]any{
			"messageId": "mem-reply-7",
			"timestamp": 1_700_000_000,
		}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"reply",
		"--to", "5511900000000@s.whatsapp.net",
		"--quoted-id", "orig-42",
		"--body", "ack",
	)

	if !strings.Contains(stdout, `"messageId":"mem-reply-7"`) {
		t.Fatalf("stdout missing messageId:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "send.reply" {
		t.Fatalf("expected 1 send.reply call, got %+v", calls)
	}
}

// TestWaGroupsGet exercises `wa groups get --jid <g>` (FR-072 / T2-11).
func TestWaGroupsGet(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("groups.get", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			JID string `json:"jid"`
		}
		_ = json.Unmarshal(params, &p)
		if p.JID == "" {
			return nil, &rpcError{Code: -32602, Message: "missing jid"}
		}
		return map[string]any{
			"jid":          p.JID,
			"subject":      "Project X",
			"participants": []string{"5511999999999@s.whatsapp.net", "5511888888888@s.whatsapp.net"},
			"admins":       []string{"5511999999999@s.whatsapp.net"},
			"fetchedAt":    1_700_000_000,
		}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"groups", "get",
		"--jid", "120363042199654321@g.us",
	)

	if !strings.Contains(stdout, `"subject":"Project X"`) {
		t.Fatalf("stdout missing subject:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, `"schema":"wa.groups.get/v1"`) {
		t.Fatalf("stdout missing schema envelope:\nstdout=%q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "groups.get" {
		t.Fatalf("expected 1 groups.get call, got %+v", calls)
	}
}
