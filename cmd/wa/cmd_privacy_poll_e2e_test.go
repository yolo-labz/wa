// Package main — end-to-end tests for the `wa privacy` and `wa poll`
// subcommand trees (PR #192). Each test stands up a fake JSON-RPC server
// on a throwaway unix socket, invokes the cobra command tree in-process,
// and asserts on the method routing, params, and output shape.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaPrivacyGet drives `wa privacy get`, asserting the method routes to
// privacy.get and that the human output prints one "key: value" line per
// setting.
func TestWaPrivacyGet(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("privacy.get", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{"settings": []map[string]string{
			{"key": "lastSeen", "value": "contacts"},
			{"key": "groups", "value": "everyone"},
		}}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "privacy", "get")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("privacy get failed: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "lastSeen: contacts") || !strings.Contains(stdout, "groups: everyone") {
		t.Fatalf("privacy get human output missing settings:\n%s", stdout)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "privacy.get" {
		t.Fatalf("calls = %+v, want one privacy.get", calls)
	}
}

// TestWaPrivacyGetKeyFilter confirms `--key` narrows the output to a single
// dimension client-side (the daemon returns the full set).
func TestWaPrivacyGetKeyFilter(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("privacy.get", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{"settings": []map[string]string{
			{"key": "lastSeen", "value": "contacts"},
			{"key": "groups", "value": "everyone"},
		}}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "privacy", "get", "--key", "lastSeen")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("privacy get --key failed: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "lastSeen: contacts") {
		t.Fatalf("filtered output missing requested key:\n%s", stdout)
	}
	if strings.Contains(stdout, "groups") {
		t.Fatalf("filtered output leaked a non-requested key:\n%s", stdout)
	}
}

// TestWaPrivacySet asserts `wa privacy set` forwards {key,value} verbatim to
// privacy.set.
func TestWaPrivacySet(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("privacy.set", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Key != "lastSeen" || p.Value != "contacts" {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t,
		"--socket", fd.path(),
		"privacy", "set", "--key", "lastSeen", "--value", "contacts",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("privacy set failed: stderr=%q", stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "privacy.set" {
		t.Fatalf("calls = %+v, want one privacy.set", calls)
	}
}

// TestWaPrivacySetRequiresFlags confirms MarkFlagRequired fires when --value
// is omitted: cobra errors before any RPC is sent.
func TestWaPrivacySetRequiresFlags(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("privacy.set", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(), "privacy", "set", "--key", "lastSeen")
	if !strings.Contains(stderr, "exec error") || !strings.Contains(stderr, "value") {
		t.Fatalf("expected a required-flag error naming 'value', got stderr=%q", stderr)
	}
	if calls := fd.seen(); len(calls) != 0 {
		t.Fatalf("no RPC should fire when a required flag is missing, saw %+v", calls)
	}
}

// TestWaPollVote drives `wa poll vote` with repeatable --option flags and
// asserts the indices land in the "selected" array verbatim alongside chat
// and pollId.
func TestWaPollVote(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("poll.vote", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat     string `json:"chat"`
			PollID   string `json:"pollId"`
			Selected []int  `json:"selected"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" || p.PollID != "POLL123" {
			return nil, &rpcError{Code: -32602, Message: "wrong chat/pollId"}
		}
		if len(p.Selected) != 2 || p.Selected[0] != 0 || p.Selected[1] != 2 {
			return nil, &rpcError{Code: -32602, Message: "wrong selected"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t,
		"--socket", fd.path(),
		"poll", "vote",
		"--chat", "5511900000000@s.whatsapp.net",
		"--poll-id", "POLL123",
		"--option", "0", "--option", "2",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("poll vote failed: stderr=%q", stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "poll.vote" {
		t.Fatalf("calls = %+v, want one poll.vote", calls)
	}
}

// TestWaPollVoteRequiresFlags confirms --chat / --poll-id are required.
func TestWaPollVoteRequiresFlags(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("poll.vote", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(), "poll", "vote", "--chat", "x@s.whatsapp.net")
	if !strings.Contains(stderr, "exec error") || !strings.Contains(stderr, "poll-id") {
		t.Fatalf("expected a required-flag error naming 'poll-id', got stderr=%q", stderr)
	}
	if calls := fd.seen(); len(calls) != 0 {
		t.Fatalf("no RPC should fire when a required flag is missing, saw %+v", calls)
	}
}
