// Package main — end-to-end tests for the universal `--chat` recipient
// alias (#194, audit finding #8). The recipient JID arg is spelled four
// ways across the CLI: --to, --chat, --jid, --group. PR #194 registers
// `--chat` as an accepted synonym on every command whose canonical
// recipient flag is --to / --jid / --group, via a pflag normalization
// func (see flag_alias.go). These tests prove, for a representative
// command in each of the three alias groups, that:
//
//  1. invoking with `--chat <jid>` lands the recipient in the RPC params
//     under the canonical key, AND
//  2. invoking with the original flag (--to / --jid / --group) still works.
//
// Each test stands up a fake JSON-RPC daemon (shared harness from
// cmd_t121_e2e_test.go) and asserts on the recorded params.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// paramString extracts a single string field from the params of the first
// recorded RPC call, failing the test if the shape is wrong.
func paramString(t *testing.T, calls []rpcCall, wantMethod, key string) string {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("want exactly one RPC call, got %d: %+v", len(calls), calls)
	}
	if calls[0].Method != wantMethod {
		t.Fatalf("method = %q, want %q", calls[0].Method, wantMethod)
	}
	var m map[string]any
	if err := json.Unmarshal(calls[0].Params, &m); err != nil {
		t.Fatalf("params not an object: %v (raw=%s)", err, calls[0].Params)
	}
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("params[%q] missing or not a string: %+v", key, m)
	}
	return v
}

// --- group "to" : `wa send` ------------------------------------------------

// TestChatAliasSend_To proves `wa send` accepts both --to and --chat and
// that either spelling lands the recipient in the "to" RPC param.
func TestChatAliasSend_To(t *testing.T) {
	const jid = "5511900000000@s.whatsapp.net"

	t.Run("canonical --to still works", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("send", func(json.RawMessage) (any, *rpcError) { return map[string]any{"id": "X"}, nil })

		_, stderr := runCmd(t, "--socket", fd.path(), "send", "--to", jid, "--body", "hi")
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("send --to failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "send", "to"); got != jid {
			t.Fatalf(`params["to"] = %q, want %q`, got, jid)
		}
	})

	t.Run("--chat alias accepted as --to", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("send", func(json.RawMessage) (any, *rpcError) { return map[string]any{"id": "X"}, nil })

		_, stderr := runCmd(t, "--socket", fd.path(), "send", "--chat", jid, "--body", "hi")
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("send --chat failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "send", "to"); got != jid {
			t.Fatalf(`--chat did not map to params["to"]: got %q, want %q`, got, jid)
		}
	})
}

// --- group "jid" : `wa contact block` -------------------------------------

// TestChatAliasContactBlock_JID proves `wa contact block` accepts both
// --jid and --chat, landing the recipient in the "jid" RPC param.
func TestChatAliasContactBlock_JID(t *testing.T) {
	const jid = "5511911111111@s.whatsapp.net"

	t.Run("canonical --jid still works", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("contact.block", func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil })

		_, stderr := runCmd(t, "--socket", fd.path(), "contact", "block", "--jid", jid)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("contact block --jid failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "contact.block", "jid"); got != jid {
			t.Fatalf(`params["jid"] = %q, want %q`, got, jid)
		}
	})

	t.Run("--chat alias accepted as --jid", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("contact.block", func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil })

		_, stderr := runCmd(t, "--socket", fd.path(), "contact", "block", "--chat", jid)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("contact block --chat failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "contact.block", "jid"); got != jid {
			t.Fatalf(`--chat did not map to params["jid"]: got %q, want %q`, got, jid)
		}
	})
}

// TestChatAliasGroupsGet_JID proves `wa groups get` (a second --jid command)
// accepts --chat as a synonym for --jid.
func TestChatAliasGroupsGet_JID(t *testing.T) {
	const jid = "120363000000000000@g.us"

	t.Run("canonical --jid still works", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("groups.get", func(json.RawMessage) (any, *rpcError) {
			return map[string]any{"jid": jid, "subject": "S"}, nil
		})

		_, stderr := runCmd(t, "--socket", fd.path(), "groups", "get", "--jid", jid)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("groups get --jid failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "groups.get", "jid"); got != jid {
			t.Fatalf(`params["jid"] = %q, want %q`, got, jid)
		}
	})

	t.Run("--chat alias accepted as --jid", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("groups.get", func(json.RawMessage) (any, *rpcError) {
			return map[string]any{"jid": jid, "subject": "S"}, nil
		})

		_, stderr := runCmd(t, "--socket", fd.path(), "groups", "get", "--chat", jid)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("groups get --chat failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "groups.get", "jid"); got != jid {
			t.Fatalf(`--chat did not map to params["jid"]: got %q, want %q`, got, jid)
		}
	})
}

// --- group "group" : `wa group add` (roster op) ---------------------------

// TestChatAliasGroupAdd_Group proves a roster op (`wa group add`) accepts
// both --group and --chat, landing the recipient in the "group" RPC param.
// This covers the bindRosterFlags path shared by add/remove/promote/demote.
func TestChatAliasGroupAdd_Group(t *testing.T) {
	const grp = "120363000000000000@g.us"
	const member = "5511922222222@s.whatsapp.net"

	t.Run("canonical --group still works", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("group.addParticipants", func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil })

		_, stderr := runCmd(t, "--socket", fd.path(),
			"group", "add", "--group", grp, "--participant", member)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("group add --group failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "group.addParticipants", "group"); got != grp {
			t.Fatalf(`params["group"] = %q, want %q`, got, grp)
		}
	})

	t.Run("--chat alias accepted as --group", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("group.addParticipants", func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil })

		_, stderr := runCmd(t, "--socket", fd.path(),
			"group", "add", "--chat", grp, "--participant", member)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("group add --chat failed: %s", stderr)
		}
		if got := paramString(t, fd.seen(), "group.addParticipants", "group"); got != grp {
			t.Fatalf(`--chat did not map to params["group"]: got %q, want %q`, got, grp)
		}
	})
}

// TestChatAliasDoesNotClobberOtherFlags is a regression guard: the
// normalize func must rewrite ONLY "chat", leaving every other flag name
// untouched. `wa group add --chat <grp> --participant <m>` must still parse
// --participant correctly (a distinct flag bound on the same command).
//
// Note: --participant is a StringSliceVar whose global accumulates across
// the in-process harness's runCmd invocations (a known reset limitation of
// resetAllFlags for slice flags). So we assert the participant we passed is
// PRESENT in the RPC params — proving the normalizer did not rename
// --participant to something else — rather than asserting an exact slice
// length, which would be order-dependent.
func TestChatAliasDoesNotClobberOtherFlags(t *testing.T) {
	const grp = "120363000000000000@g.us"
	const member = "5511933333333@s.whatsapp.net"

	fd := newFakeDaemon(t)
	fd.on("group.addParticipants", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Group        string   `json:"group"`
			Participants []string `json:"participants"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Group != grp {
			return nil, &rpcError{Code: -32602, Message: "wrong group"}
		}
		found := false
		for _, m := range p.Participants {
			if m == member {
				found = true
			}
		}
		if !found {
			return nil, &rpcError{Code: -32602, Message: "participant clobbered by normalize func"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(),
		"group", "add", "--chat", grp, "--participant", member)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("group add --chat --participant failed (normalize func clobbered a sibling flag?): %s", stderr)
	}
	if calls := fd.seen(); len(calls) != 1 {
		t.Fatalf("want one group.addParticipants call, got %+v", calls)
	}
}
