package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCLI_ScheduleResolveSendsIDAndOutcome: `wa schedule resolve <id>
// --outcome` forwards both; an outcome that is not sent|not_sent is refused
// before any call.
func TestCLI_ScheduleResolveSendsIDAndOutcome(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("schedule.resolve", func(params json.RawMessage) (any, *rpcError) {
		var p struct{ ID, Outcome string }
		_ = json.Unmarshal(params, &p)
		return map[string]any{"schedule": map[string]any{"id": p.ID, "state": "fired", "outcome": p.Outcome}}, nil
	})
	_, stderr := runCmd(t, "--socket", fd.path(), "schedule", "resolve", "u1", "--outcome", "maybe")
	if !strings.Contains(stderr, "--outcome must be sent or not_sent") || calls(fd) != 0 {
		t.Fatalf("bad outcome: stderr %q, %d calls; want a refusal and no call", stderr, calls(fd))
	}
	stdout, stderr := runCmd(t, "--socket", fd.path(), "--json", "schedule", "resolve", "u1", "--outcome", "not_sent")
	if !strings.Contains(stdout, `"id":"u1"`) || !strings.Contains(stdout, `"outcome":"not_sent"`) || calls(fd) != 1 {
		t.Fatalf("stdout %q stderr %q, %d calls; want id and outcome forwarded once", stdout, stderr, calls(fd))
	}
}

func calls(fd *fakeDaemon) (n int) {
	for _, c := range fd.seen() {
		if c.Method == "schedule.resolve" {
			n++
		}
	}
	return n
}
