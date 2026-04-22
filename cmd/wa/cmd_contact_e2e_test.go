// Package main — end-to-end tests for feature 018 T2-10 `wa contact`
// subcommand tree (block, unblock, blocklist). Matches tasks-tier2.md
// T2-10 gate test: TestWaContactBlockUnblockRoundTrip.
package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// TestWaContactBlockUnblockRoundTrip drives `wa contact block` →
// `wa contact blocklist` → `wa contact unblock` → `wa contact blocklist`
// against a fake daemon, verifying FR-018/FR-019 method routing and
// --json output shape.
func TestWaContactBlockUnblockRoundTrip(t *testing.T) {
	const testJID = "5511900000000@s.whatsapp.net"

	fd := newFakeDaemon(t)

	var (
		mu     sync.Mutex
		listed = make(map[string]struct{})
	)

	fd.on("contact.block", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			JID string `json:"jid"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.JID != testJID {
			return nil, &rpcError{Code: -32602, Message: "wrong jid"}
		}
		mu.Lock()
		listed[p.JID] = struct{}{}
		mu.Unlock()
		return map[string]any{}, nil
	})

	fd.on("contact.unblock", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			JID string `json:"jid"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		mu.Lock()
		delete(listed, p.JID)
		mu.Unlock()
		return map[string]any{}, nil
	})

	fd.on("contact.blocklist", func(_ json.RawMessage) (any, *rpcError) {
		mu.Lock()
		out := make([]string, 0, len(listed))
		for j := range listed {
			out = append(out, j)
		}
		mu.Unlock()
		return map[string]any{"blocked": out}, nil
	})

	// Step 1 — block.
	_, stderr := runCmd(t,
		"--socket", fd.path(), "--json",
		"contact", "block", "--jid", testJID,
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("contact block failed: stderr=%q", stderr)
	}

	// Step 2 — blocklist, expect 1 entry.
	stdout, stderr := runCmd(t,
		"--socket", fd.path(), "--json",
		"contact", "blocklist",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("contact blocklist #1 failed: stderr=%q", stderr)
	}
	var res1 struct {
		Blocked []string `json:"blocked"`
	}
	if err := json.Unmarshal([]byte(firstJSONLine(stdout)), &res1); err != nil {
		t.Fatalf("blocklist #1 decode: %v (stdout=%q)", err, stdout)
	}
	if len(res1.Blocked) != 1 || res1.Blocked[0] != testJID {
		t.Fatalf("blocklist #1 = %+v, want [%s]", res1.Blocked, testJID)
	}

	// Step 3 — unblock.
	_, stderr = runCmd(t,
		"--socket", fd.path(), "--json",
		"contact", "unblock", "--jid", testJID,
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("contact unblock failed: stderr=%q", stderr)
	}

	// Step 4 — blocklist, expect empty.
	stdout, stderr = runCmd(t,
		"--socket", fd.path(), "--json",
		"contact", "blocklist",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("contact blocklist #2 failed: stderr=%q", stderr)
	}
	var res2 struct {
		Blocked []string `json:"blocked"`
	}
	if err := json.Unmarshal([]byte(firstJSONLine(stdout)), &res2); err != nil {
		t.Fatalf("blocklist #2 decode: %v (stdout=%q)", err, stdout)
	}
	if len(res2.Blocked) != 0 {
		t.Fatalf("blocklist #2 = %+v, want empty", res2.Blocked)
	}

	calls := fd.seen()
	gotMethods := make([]string, len(calls))
	for i, c := range calls {
		gotMethods[i] = c.Method
	}
	wantOrder := []string{"contact.block", "contact.blocklist", "contact.unblock", "contact.blocklist"}
	if len(gotMethods) != len(wantOrder) {
		t.Fatalf("method sequence = %v, want %v", gotMethods, wantOrder)
	}
	for i, m := range wantOrder {
		if gotMethods[i] != m {
			t.Fatalf("call[%d] = %q, want %q (full = %v)", i, gotMethods[i], m, gotMethods)
		}
	}
}

// firstJSONLine returns the first non-empty line in s — the CLI
// prints the JSON result on its own line followed by a trailing
// newline, so the raw stdout is a single valid JSON object in the
// happy path. We strip anything trailing just in case.
func firstJSONLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && (line[0] == '{' || line[0] == '[') {
			return line
		}
	}
	return s
}
