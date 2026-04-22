// Package main — end-to-end tests for feature 018 T2-14 `wa profile`
// identity-edit subcommand tree (set-name, set-status, avatar). Matches
// tasks-tier2.md T2-14 gate tests: TestWaProfileSetName,
// TestWaProfileAvatarCachedPath.
package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// TestWaProfileSetName drives `wa profile set-name --name X` against a
// fake daemon, asserts exactly one `profile.setName` RPC carrying the
// expected name, and that --json surfaces the empty result object.
func TestWaProfileSetName(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("profile.setName", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Name != "Pedro" {
			return nil, &rpcError{Code: -32602, Message: "wrong name"}
		}
		return map[string]any{}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(), "--json",
		"profile", "set-name", "--name", "Pedro",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("set-name failed: stderr=%q", stderr)
	}
	if firstJSONLine(stdout) == "" {
		t.Fatalf("stdout has no JSON line: %q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "profile.setName" {
		t.Fatalf("expected 1 profile.setName call, got %+v", calls)
	}
}

// TestWaProfileSetStatus drives `wa profile set-status --status X` and
// asserts the RPC forwards the status verbatim.
func TestWaProfileSetStatus(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("profile.setStatus", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Status != "hello" {
			return nil, &rpcError{Code: -32602, Message: "wrong status"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t,
		"--socket", fd.path(), "--json",
		"profile", "set-status", "--status", "hello",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("set-status failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "profile.setStatus" {
		t.Fatalf("expected 1 profile.setStatus call, got %+v", calls)
	}
}

// TestWaProfileAvatarCachedPath drives two successive `wa profile avatar`
// calls. The daemon returns the same path on both calls (content-addressed
// cache hit); the CLI prints the path without a second download, and the
// fake records exactly two RPC hits — the client does not cache, the
// daemon does.
func TestWaProfileAvatarCachedPath(t *testing.T) {
	fd := newFakeDaemon(t)
	var calls atomic.Int32
	const wantPath = "/tmp/wa/media/avatars/sha256/ab/cdef.jpg"

	fd.on("contacts.profilePhoto", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			JID  string `json:"jid"`
			Size string `json:"size"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.JID != "5511999999999@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong jid"}
		}
		if p.Size != "preview" {
			return nil, &rpcError{Code: -32602, Message: "wrong size"}
		}
		calls.Add(1)
		return map[string]any{"path": wantPath}, nil
	})

	// First call — default --size is "preview" (plain text output).
	stdout1, stderr1 := runCmd(t,
		"--socket", fd.path(),
		"profile", "avatar",
		"--jid", "5511999999999@s.whatsapp.net",
	)
	if strings.Contains(stderr1, "exec error") {
		t.Fatalf("avatar #1 failed: stderr=%q", stderr1)
	}
	if !strings.Contains(stdout1, wantPath) {
		t.Fatalf("avatar #1 stdout=%q, want path=%q", stdout1, wantPath)
	}

	// Second call — same (jid, size), daemon returns same cached path.
	stdout2, stderr2 := runCmd(t,
		"--socket", fd.path(),
		"profile", "avatar",
		"--jid", "5511999999999@s.whatsapp.net",
		"--size", "preview",
	)
	if strings.Contains(stderr2, "exec error") {
		t.Fatalf("avatar #2 failed: stderr=%q", stderr2)
	}
	if strings.TrimSpace(stdout2) != wantPath {
		t.Fatalf("avatar #2 stdout=%q, want exactly %q", stdout2, wantPath)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("daemon call count = %d, want 2 (client does not cache)", got)
	}
}
