// Package main — end-to-end tests for feature 017 T3-19 CLI subcommands.
// Matches tasks-tier3.md row T3-19 test name TestWaSchedule.
package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWaSchedule covers the full `wa schedule {send,list,cancel,update}`
// surface against an in-process fakeDaemon. Verifies method dispatch,
// payload shape, and `--fire-at` parsing (RFC3339 + unix-seconds).
func TestWaSchedule(t *testing.T) {
	fd := newFakeDaemon(t)

	// --- schedule.send handler ---------------------------------------
	fd.on("schedule.send", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			To     string `json:"to"`
			Body   string `json:"body"`
			FireAt int64  `json:"fireAt"`
			ID     string `json:"id"`
			Kind   string `json:"kind"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.To == "" || p.FireAt <= 0 {
			return nil, &rpcError{Code: -32602, Message: "to/fireAt required"}
		}
		return map[string]any{
			"schedule": map[string]any{
				"id":        "sch-abc",
				"profile":   "default",
				"kind":      "send_text",
				"recipient": p.To,
				"body":      p.Body,
				"fireAt":    p.FireAt,
				"state":     "pending",
			},
		}, nil
	})

	// --- schedule.list handler ---------------------------------------
	fd.on("schedule.list", func(params json.RawMessage) (any, *rpcError) {
		return map[string]any{
			"schedules": []any{
				map[string]any{
					"id":     "sch-abc",
					"state":  "pending",
					"fireAt": time.Now().Add(time.Hour).Unix(),
				},
			},
		}, nil
	})

	// --- schedule.cancel handler -------------------------------------
	fd.on("schedule.cancel", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ID == "" {
			return nil, &rpcError{Code: -32602, Message: "id required"}
		}
		return map[string]any{
			"schedule": map[string]any{"id": p.ID, "state": "cancelled"},
		}, nil
	})

	// --- schedule.update handler -------------------------------------
	fd.on("schedule.update", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			ID     string `json:"id"`
			FireAt int64  `json:"fireAt"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ID == "" || p.FireAt <= 0 {
			return nil, &rpcError{Code: -32602, Message: "id/fireAt required"}
		}
		return map[string]any{
			"schedule": map[string]any{
				"id":     p.ID,
				"state":  "pending",
				"fireAt": p.FireAt,
			},
		}, nil
	})

	// --- 1. send with unix-seconds fireAt ----------------------------
	fireUnix := time.Now().Add(2 * time.Hour).Unix()
	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"schedule", "send",
		"--to", "5511999999999",
		"--body", "hi",
		"--fire-at", strconv.FormatInt(fireUnix, 10),
	)
	if !strings.Contains(stdout, `"schema":"wa.schedule.send/v1"`) {
		t.Fatalf("send stdout missing schema:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, `"state":"pending"`) {
		t.Fatalf("send stdout missing state=pending: %q", stdout)
	}

	// --- 2. send with RFC3339 fireAt (verifies parseFireAt fallback) -
	rfc := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	_, stderr2 := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"schedule", "send",
		"--to", "5511999999999",
		"--body", "hi-rfc",
		"--fire-at", rfc,
	)
	if stderr2 != "" {
		t.Fatalf("send --fire-at RFC3339 failed: %q", stderr2)
	}

	// --- 3. list ------------------------------------------------------
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"schedule", "list",
	)
	if !strings.Contains(stdout, `"sch-abc"`) {
		t.Fatalf("list stdout missing id: %q", stdout)
	}

	// --- 4. cancel ----------------------------------------------------
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"schedule", "cancel",
		"--id", "sch-abc",
	)
	if !strings.Contains(stdout, `"state":"cancelled"`) {
		t.Fatalf("cancel stdout missing cancelled: %q", stdout)
	}

	// --- 5. update ---------------------------------------------------
	newFire := time.Now().Add(8 * time.Hour).Unix()
	stdout, _ = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"schedule", "update",
		"--id", "sch-abc",
		"--fire-at", strconv.FormatInt(newFire, 10),
	)
	if !strings.Contains(stdout, `"fireAt":`+strconv.FormatInt(newFire, 10)) {
		t.Fatalf("update stdout missing new fireAt: %q", stdout)
	}

	// --- method sequence sanity --------------------------------------
	calls := fd.seen()
	wantSeq := []string{"schedule.send", "schedule.send", "schedule.list", "schedule.cancel", "schedule.update"}
	if len(calls) != len(wantSeq) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(wantSeq), calls)
	}
	for i, want := range wantSeq {
		if calls[i].Method != want {
			t.Fatalf("call %d: got %q want %q", i, calls[i].Method, want)
		}
	}
}

// TestWaScheduleSendMissingFlags verifies --to/--fire-at are enforced
// client-side (exit 64 / sysexits EX_USAGE).
func TestWaScheduleSendMissingFlags(t *testing.T) {
	// Reset package-level flag state that previous tests may have set.
	scheduleTo = ""
	scheduleFireAt = ""
	scheduleBody = ""
	scheduleID = ""

	_, stderr := runCmd(t, "schedule", "send")
	if !strings.Contains(stderr, "--to and --fire-at are required") {
		t.Fatalf("expected usage error, got stderr=%q", stderr)
	}
}
