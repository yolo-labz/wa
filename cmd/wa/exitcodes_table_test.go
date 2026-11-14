package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// TestExitTable_JobSendCodes pins the numeric table of the codes the job
// executor (-32021, -32022) and send safety (-32023) added: the catalog name,
// the CLI exit, and a hint that never invites a resend of a send whose
// outcome is unknown. Exits 75 and 130 are raised by jobs.go, not by an RPC
// code, so only 75's constant is pinned here.
func TestExitTable_JobSendCodes(t *testing.T) {
	t.Parallel()

	var catalog struct {
		Errors []struct {
			Code int    `json:"code"`
			Name string `json:"name"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(agentdocs.ErrorsJSON, &catalog); err != nil {
		t.Fatalf("parse errors.json: %v", err)
	}
	names := make(map[int]string, len(catalog.Errors))
	for _, e := range catalog.Errors {
		names[e.Code] = e.Name
	}

	for _, tc := range []struct {
		code int
		name string
		exit int
	}{
		{-32021, "job_not_found", exitUsage},
		{-32022, "deadline_exceeded", exitGeneric},
		{-32023, "send_uncertain", exitGeneric},
	} {
		if got := names[tc.code]; got != tc.name {
			t.Errorf("errors.json names %d %q, want %q", tc.code, got, tc.name)
		}
		if got := rpcCodeToExit(tc.code); got != tc.exit {
			t.Errorf("%s (%d): exit %d, want %d", tc.name, tc.code, got, tc.exit)
		}
		if h := hintForRPCCode(tc.code); !strings.Contains(h, "do not resend") {
			t.Errorf("%s (%d): hint %q must say do not resend", tc.name, tc.code, h)
		}
	}
	if exitOutcomeUnknown != 75 {
		t.Errorf("exitOutcomeUnknown = %d, want 75 (EX_TEMPFAIL)", exitOutcomeUnknown)
	}
}
