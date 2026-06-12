package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeWiper records the Panic call and returns a configurable error.
type fakeWiper struct {
	reasons []string
	err     error
}

func (f *fakeWiper) Panic(_ context.Context, reason string) error {
	f.reasons = append(f.reasons, reason)
	return f.err
}

// capturingAudit stores every recorded event for assertion.
type capturingAudit struct {
	events []domain.AuditEvent
}

func (c *capturingAudit) Record(_ context.Context, e domain.AuditEvent) error {
	c.events = append(c.events, e)
	return nil
}

func panicCall(t *testing.T, w *fakeWiper, audit interface {
	Record(context.Context, domain.AuditEvent) error
},
) (map[string]any, error) {
	t.Helper()
	h := handlePanic(w, nil, audit, slog.New(slog.DiscardHandler))
	raw, err := h(context.Background(), nil)
	var res map[string]any
	if raw != nil {
		if uerr := json.Unmarshal(raw, &res); uerr != nil {
			t.Fatalf("unmarshal panic result: %v", uerr)
		}
	}
	return res, err
}

// TestPanicHappyPath asserts TEST-17 at the wad layer: the RPC wipes
// with reason "rpc", mirrors a durable AuditPanic "wiped" row, and
// reports unlinked:true.
func TestPanicHappyPath(t *testing.T) {
	w := &fakeWiper{}
	audit := &capturingAudit{}

	res, err := panicCall(t, w, audit)
	if err != nil {
		t.Fatalf("panic RPC: %v", err)
	}
	if res["unlinked"] != true {
		t.Errorf("unlinked = %v, want true", res["unlinked"])
	}
	if len(w.reasons) != 1 || w.reasons[0] != "rpc" {
		t.Errorf("wiper calls = %v, want one call with reason \"rpc\"", w.reasons)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	evt := audit.events[0]
	if evt.Action != domain.AuditPanic || evt.Decision != "wiped" {
		t.Errorf("audit row = action %v decision %q, want AuditPanic/wiped", evt.Action, evt.Decision)
	}
}

// TestPanicWipeErrorStillSucceeds pins the R-07 always-success contract:
// a wipe that reports filesystem errors must not fail the RPC, and the
// durable audit row is still written.
func TestPanicWipeErrorStillSucceeds(t *testing.T) {
	w := &fakeWiper{err: errors.New("session.db: permission denied")}
	audit := &capturingAudit{}

	res, err := panicCall(t, w, audit)
	if err != nil {
		t.Fatalf("panic RPC must swallow wipe errors, got %v", err)
	}
	if res["unlinked"] != true {
		t.Errorf("unlinked = %v, want true despite wipe error", res["unlinked"])
	}
	if len(audit.events) != 1 {
		t.Errorf("audit events = %d, want 1 despite wipe error", len(audit.events))
	}
}

// TestPanicAuditFailureStillSucceeds pins the SF-01 exemption: the wipe
// already happened, so a failed durable audit row must not fail the RPC
// (recovery intent takes precedence; log is best effort).
func TestPanicAuditFailureStillSucceeds(t *testing.T) {
	w := &fakeWiper{}

	res, err := panicCall(t, w, failAudit{})
	if err != nil {
		t.Fatalf("panic RPC must swallow audit errors, got %v", err)
	}
	if res["unlinked"] != true {
		t.Errorf("unlinked = %v, want true despite audit failure", res["unlinked"])
	}
	if len(w.reasons) != 1 {
		t.Errorf("wiper calls = %d, want 1", len(w.reasons))
	}
}
