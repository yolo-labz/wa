package main

import (
	"context"
	"testing"
	"time"
)

// TestDoctorUnder3s asserts runDoctorChecks returns well under the
// 3-second SLA for a clean install (no daemon → fast FAIL on pairing).
func TestDoctorUnder3s(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	checks := runDoctorChecks(ctx)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("doctor took %s (>3s SLA)", elapsed)
	}
	if len(checks) != 11 {
		t.Errorf("got %d checks, want 11", len(checks))
	}
}

// TestDoctorAll11Checks asserts every FR-041 check is present with a
// stable name, so future refactors can't silently drop one.
func TestDoctorAll11Checks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	want := []string{
		"pairing", "socket", "lockfile", "schema_version",
		"audit_log_size", "recent_critical", "whatsmeow_pin",
		"otel_exporter", "migration_backups", "whatsmeow_stale",
		"renovate_freshness",
	}
	checks := runDoctorChecks(ctx)
	if len(checks) != len(want) {
		t.Fatalf("got %d checks, want %d", len(checks), len(want))
	}
	for i, c := range checks {
		if c.Name != want[i] {
			t.Errorf("check[%d].Name = %q, want %q", i, c.Name, want[i])
		}
		switch c.Status {
		case doctorOK, doctorWARN, doctorFAIL:
		default:
			t.Errorf("check[%d] (%s) bogus status %q", i, c.Name, c.Status)
		}
	}
}

// TestCountFails verifies countFails matches doctorFAIL entries exactly.
func TestCountFails(t *testing.T) {
	cs := []doctorCheck{
		{Status: doctorOK},
		{Status: doctorFAIL},
		{Status: doctorWARN},
		{Status: doctorFAIL},
	}
	if n := countFails(cs); n != 2 {
		t.Errorf("countFails = %d, want 2", n)
	}
}
