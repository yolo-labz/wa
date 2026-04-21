// Package main — end-to-end tests for feature 017 T3-23 CLI subcommands.
// Matches tasks-tier3.md row T3-23 test name TestWaLabels.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaLabels covers `wa labels {list,create,delete,assign,unassign}`
// against an in-process fakeDaemon. Verifies method dispatch, payload
// shape, and the -32114 labels_unsupported bubble-up for personal
// accounts / flag-off daemons.
func TestWaLabels(t *testing.T) {
	fd := newFakeDaemon(t)

	fd.on("labels.list", func(params json.RawMessage) (any, *rpcError) {
		return map[string]any{
			"labels": []any{
				map[string]any{"id": "lbl_1", "name": "Work", "colorIndex": 3},
			},
		}, nil
	})

	fd.on("labels.create", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Name       string `json:"name"`
			ColorIndex int    `json:"colorIndex"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "name required"}
		}
		return map[string]any{
			"label": map[string]any{"id": "lbl_new", "name": p.Name, "colorIndex": p.ColorIndex},
		}, nil
	})

	fd.on("labels.delete", func(params json.RawMessage) (any, *rpcError) {
		return map[string]any{"deleted": true}, nil
	})

	fd.on("labels.assign", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			LabelID   string `json:"labelID"`
			Chat      string `json:"chat"`
			MessageID string `json:"messageID"`
		}
		_ = json.Unmarshal(params, &p)
		if p.LabelID == "" || p.Chat == "" {
			return nil, &rpcError{Code: -32602, Message: "labelID/chat required"}
		}
		return map[string]any{
			"assignment": map[string]any{
				"labelID":    p.LabelID,
				"chat":       p.Chat,
				"messageID":  p.MessageID,
				"targetKind": "chat",
			},
		}, nil
	})

	fd.on("labels.unassign", func(params json.RawMessage) (any, *rpcError) {
		return map[string]any{"unassigned": true}, nil
	})

	// --- 1. list ------------------------------------------------------
	stdout, _ := runCmd(t, "--socket", fd.path(), "--json", "labels", "list")
	if !strings.Contains(stdout, `"schema":"wa.labels.list/v1"`) {
		t.Fatalf("list stdout missing schema: %q", stdout)
	}
	if !strings.Contains(stdout, `"lbl_1"`) {
		t.Fatalf("list stdout missing lbl_1: %q", stdout)
	}

	// --- 2. create ----------------------------------------------------
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"labels", "create",
		"--name", "Leads",
		"--color", "4",
	)
	if !strings.Contains(stdout, `"lbl_new"`) || !strings.Contains(stdout, `"Leads"`) {
		t.Fatalf("create stdout missing new label: %q", stdout)
	}

	// --- 3. delete ----------------------------------------------------
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"labels", "delete",
		"--id", "lbl_new",
	)
	if !strings.Contains(stdout, `"deleted":true`) {
		t.Fatalf("delete stdout missing deleted=true: %q", stdout)
	}

	// --- 4. assign chat-level ----------------------------------------
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"labels", "assign",
		"--label-id", "lbl_1",
		"--chat", "5511999999999@s.whatsapp.net",
	)
	if !strings.Contains(stdout, `"labelID":"lbl_1"`) {
		t.Fatalf("assign stdout missing labelID: %q", stdout)
	}

	// --- 5. unassign --------------------------------------------------
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"labels", "unassign",
		"--label-id", "lbl_1",
		"--chat", "5511999999999@s.whatsapp.net",
		"--message-id", "msg_01",
	)
	if !strings.Contains(stdout, `"unassigned":true`) {
		t.Fatalf("unassign stdout missing unassigned=true: %q", stdout)
	}

	// --- method sequence sanity --------------------------------------
	calls := fd.seen()
	wantSeq := []string{"labels.list", "labels.create", "labels.delete", "labels.assign", "labels.unassign"}
	if len(calls) != len(wantSeq) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(wantSeq), calls)
	}
	for i, want := range wantSeq {
		if calls[i].Method != want {
			t.Fatalf("call %d: got %q want %q", i, calls[i].Method, want)
		}
	}
}

// TestWaLabelsUnsupportedBubbles verifies the -32114 labels_unsupported
// RPC error surfaces as a non-empty stderr when the daemon refuses the
// call (personal account or flag off).
func TestWaLabelsUnsupportedBubbles(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("labels.list", func(params json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32114, Message: "labels unsupported"}
	})

	_, stderr := runCmd(t, "--socket", fd.path(), "labels", "list")
	if !strings.Contains(stderr, "labels unsupported") {
		t.Fatalf("expected labels_unsupported in stderr, got %q", stderr)
	}
}

// TestWaLabelsCreateMissingName verifies --name is enforced client-side.
func TestWaLabelsCreateMissingName(t *testing.T) {
	// Reset package-level flag state carried across tests.
	labelsName = ""
	labelsColor = 0

	_, stderr := runCmd(t, "labels", "create")
	if !strings.Contains(stderr, "--name is required") {
		t.Fatalf("expected usage error, got stderr=%q", stderr)
	}
}

// TestWaLabelsAssignMissingFlags verifies --label-id and --chat are both
// required by the CLI before the socket is dialed.
func TestWaLabelsAssignMissingFlags(t *testing.T) {
	labelsLabelID = ""
	labelsChat = ""
	labelsMessageID = ""

	_, stderr := runCmd(t, "labels", "assign")
	if !strings.Contains(stderr, "--label-id and --chat are required") {
		t.Fatalf("expected usage error, got stderr=%q", stderr)
	}
}
