package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// failingPairer satisfies app.Pairer with a fixed error so the
// pair-failure audit path is reachable (the memory adapter's Pair
// always succeeds).
type failingPairer struct{ err error }

func (f failingPairer) Pair(context.Context, string) error { return f.err }

// newPairDispatcher builds a dispatcher whose Pairer can be swapped
// while every other port stays on the memory adapter. A nil pairer
// keeps the adapter (always-succeeds) implementation.
func newPairDispatcher(t *testing.T, pairer app.Pairer) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:    adapter,
		Events:    adapter,
		Contacts:  adapter,
		Groups:    adapter,
		Session:   adapter,
		Allowlist: adapter,
		Audit:     adapter,
		History:   adapter,
		Pairer:    adapter,
		Quoted:    adapter,
	}
	if pairer != nil {
		cfg.Pairer = pairer
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// TestPair_FreshSession pins FR-023/FR-026: with a zero session, pair
// succeeds, reports paired=true, and writes an "ok" audit entry under
// the pair action.
func TestPair_FreshSession(t *testing.T) {
	d, adapter := newPairDispatcher(t, nil)

	result, err := d.Handle(context.Background(), "pair", nil)
	if err != nil {
		t.Fatalf("Handle(pair): %v", err)
	}
	var res struct {
		Paired bool   `json:"paired"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !res.Paired {
		t.Error("paired = false, want true")
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Action != domain.AuditPair {
		t.Errorf("audit action = %v, want pair", entries[0].Action)
	}
	if entries[0].Decision != "ok" {
		t.Errorf("audit decision = %q, want ok", entries[0].Decision)
	}
}

// TestPair_AlreadyPaired pins FR-025: a non-zero session refuses the
// pair call with ErrNotPaired and audits the denial.
func TestPair_AlreadyPaired(t *testing.T) {
	d, adapter := newPairDispatcher(t, nil)

	sess, err := domain.NewSession(domain.MustJID(testJIDStr), 1, time.Now())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := adapter.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	_, err = d.Handle(context.Background(), "pair", nil)
	if !errors.Is(err, app.ErrNotPaired) {
		t.Fatalf("err = %v, want ErrNotPaired", err)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "denied:already-paired" {
		t.Fatalf("audit = %+v, want one denied:already-paired entry", entries)
	}
}

// TestPair_PairerError pins the failure branch: an adapter-level
// pairing error propagates verbatim and audits "failed:<err>".
func TestPair_PairerError(t *testing.T) {
	pairErr := errors.New("qr channel refused")
	d, adapter := newPairDispatcher(t, failingPairer{err: pairErr})

	_, err := d.Handle(context.Background(), "pair", nil)
	if !errors.Is(err, pairErr) {
		t.Fatalf("err = %v, want the pairer's error", err)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "failed:qr channel refused" {
		t.Fatalf("audit = %+v, want one failed:qr channel refused entry", entries)
	}
}

// TestPair_SessionWipedGetsItsOwnCode pins the issue #310 translation.
// The whatsmeow adapter reports a self-retired device store with a plain
// domain sentinel, which carries no RPC code of its own: left alone it
// reaches the wire as -32603 "internal error", the same undiagnosable
// answer issue #312 removed from the media path. doPair must map it to
// -32019 so the caller can tell "restart the daemon" apart from "the
// daemon is broken".
func TestPair_SessionWipedGetsItsOwnCode(t *testing.T) {
	wiped := fmt.Errorf("pair: %w: restart the daemon", domain.ErrSessionWiped)
	d, adapter := newPairDispatcher(t, failingPairer{err: wiped})

	_, err := d.Handle(context.Background(), "pair", nil)
	if !errors.Is(err, app.ErrSessionWiped) {
		t.Fatalf("err = %v, want app.ErrSessionWiped", err)
	}
	code, ok := app.IsCodedError(err)
	if !ok || code != -32019 {
		t.Fatalf("IsCodedError = (%d, %v), want (-32019, true)", code, ok)
	}
	// Distinct from ErrNotPaired, which means the opposite (a session is
	// present and must be unlinked first).
	if errors.Is(err, app.ErrNotPaired) {
		t.Error("session-wiped refusal must not also read as ErrNotPaired")
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision == "ok" {
		t.Fatalf("audit = %+v, want one failed entry", entries)
	}
}

// TestPair_BadParams pins that malformed params are refused before any
// port is touched.
func TestPair_BadParams(t *testing.T) {
	d, adapter := newPairDispatcher(t, nil)

	_, err := d.Handle(context.Background(), "pair", json.RawMessage(`{not json`))
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
	if n := len(adapter.AuditEntries()); n != 0 {
		t.Errorf("audit entries = %d, want 0 (refused pre-pipeline)", n)
	}
}
