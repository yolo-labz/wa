// End-to-end tests for `wa group ...` (feature 018 T2-19). Each test
// stands up a fake JSON-RPC server, invokes the cobra tree in-process,
// asserts exactly one RPC with the expected method + param shape.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWaGroupCreate(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("group.create", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Subject      string   `json:"subject"`
			Participants []string `json:"participants"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Subject != "Project X" || len(p.Participants) != 2 {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		return map[string]any{
			"jid":          "120363042199654321@g.us",
			"subject":      p.Subject,
			"participants": p.Participants,
		}, nil
	})

	stdout, _ := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"group", "create",
		"--subject", "Project X",
		"--participant", "5511900000000@s.whatsapp.net",
		"--participant", "5511900000001@s.whatsapp.net",
	)
	if !strings.Contains(stdout, `"jid":"120363042199654321@g.us"`) {
		t.Fatalf("stdout missing jid: %q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "group.create" {
		t.Fatalf("want 1 group.create call, got %+v", calls)
	}
}

func TestWaGroupLeave(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("group.leave", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Group string `json:"group"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Group == "" {
			return nil, &rpcError{Code: -32602, Message: "missing group"}
		}
		return struct{}{}, nil
	})
	runCmd(t, "--socket", fd.path(), "group", "leave", "--group", "120363042199654321@g.us")
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "group.leave" {
		t.Fatalf("want 1 group.leave call, got %+v", calls)
	}
}

// TestWaGroupRoster covers add/remove/promote/demote in one table — they
// share the same flag shape and only differ in the method name.
func TestWaGroupRoster(t *testing.T) {
	cases := []struct {
		sub    string
		method string
	}{
		{"add", "group.addParticipants"},
		{"remove", "group.removeParticipants"},
		{"promote", "group.promote"},
		{"demote", "group.demote"},
	}
	for _, tc := range cases {
		t.Run(tc.sub, func(t *testing.T) {
			fd := newFakeDaemon(t)
			fd.on(tc.method, func(params json.RawMessage) (any, *rpcError) {
				var p struct {
					Group        string   `json:"group"`
					Participants []string `json:"participants"`
				}
				_ = json.Unmarshal(params, &p)
				if p.Group == "" || len(p.Participants) == 0 {
					return nil, &rpcError{Code: -32602, Message: "missing fields"}
				}
				return struct{}{}, nil
			})
			runCmd(t,
				"--socket", fd.path(),
				"group", tc.sub,
				"--group", "120363042199654321@g.us",
				"--participant", "5511900000000@s.whatsapp.net",
			)
			calls := fd.seen()
			if len(calls) != 1 || calls[0].Method != tc.method {
				t.Fatalf("want 1 %s call, got %+v", tc.method, calls)
			}
		})
	}
}

func TestWaGroupEditMultipleFields(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("group.edit", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Group       string  `json:"group"`
			Subject     *string `json:"subject"`
			Description *string `json:"description"`
			IconPath    *string `json:"iconPath"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Group == "" || p.Subject == nil || p.Description == nil {
			return nil, &rpcError{Code: -32602, Message: "missing fields"}
		}
		if p.IconPath == nil || *p.IconPath != "" {
			return nil, &rpcError{Code: -32602, Message: "expected empty-string iconPath (remove)"}
		}
		return struct{}{}, nil
	})
	runCmd(t,
		"--socket", fd.path(),
		"group", "edit",
		"--group", "120363042199654321@g.us",
		"--subject", "New Subj",
		"--description", "New Desc",
		"--remove-icon",
	)
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "group.edit" {
		t.Fatalf("want 1 group.edit call, got %+v", calls)
	}
}

func TestWaGroupEditRejectsMutuallyExclusiveIconFlags(t *testing.T) {
	fd := newFakeDaemon(t)
	// No handler registered — we expect the CLI to reject before dialling.
	_, stderr := runCmd(t,
		"--socket", fd.path(),
		"group", "edit",
		"--group", "120363042199654321@g.us",
		"--icon-path", "/tmp/x.jpg",
		"--remove-icon",
	)
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("stderr missing mutex error: %q", stderr)
	}
	if len(fd.seen()) != 0 {
		t.Fatalf("CLI must not call daemon on validation failure")
	}
}

func TestWaGroupInviteGet(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("group.inviteGet", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Group string `json:"group"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Group == "" {
			return nil, &rpcError{Code: -32602, Message: "missing group"}
		}
		return map[string]any{
			"group": p.Group,
			"url":   "https://chat.whatsapp.com/ABC123",
			"code":  "ABC123",
		}, nil
	})
	stdout, _ := runCmd(t,
		"--socket", fd.path(),
		"group", "invite", "get",
		"--group", "120363042199654321@g.us",
	)
	if !strings.Contains(stdout, "https://chat.whatsapp.com/ABC123") {
		t.Fatalf("stdout missing URL: %q", stdout)
	}
}

func TestWaGroupInviteRevokeEmitsIdempotencyKey(t *testing.T) {
	fd := newFakeDaemon(t)
	var sawKey string
	fd.on("group.inviteRevoke", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Group          string `json:"group"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		_ = json.Unmarshal(params, &p)
		sawKey = p.IdempotencyKey
		return map[string]any{
			"group": p.Group,
			"url":   "https://chat.whatsapp.com/FRESH9",
			"code":  "FRESH9",
		}, nil
	})
	runCmd(t,
		"--socket", fd.path(),
		"group", "invite", "revoke",
		"--group", "120363042199654321@g.us",
		"--idempotency-key", "abc-123",
	)
	if sawKey != "abc-123" {
		t.Fatalf("idempotencyKey not forwarded; saw %q", sawKey)
	}
}

func TestWaGroupInviteJoin(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("group.inviteJoin", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(params, &p)
		if p.URL != "https://chat.whatsapp.com/CODE42" {
			return nil, &rpcError{Code: -32602, Message: "wrong url"}
		}
		return map[string]any{"jid": "120363042199654321@g.us"}, nil
	})
	stdout, _ := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"group", "invite", "join",
		"--url", "https://chat.whatsapp.com/CODE42",
	)
	if !strings.Contains(stdout, `"jid":"120363042199654321@g.us"`) {
		t.Fatalf("stdout missing jid: %q", stdout)
	}
}
