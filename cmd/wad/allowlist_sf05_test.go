// Package main — SF-05 tests.
//
// An allowlist edit that fails to parse keeps the previous policy
// enforced (correct fail-safe), but the watcher path used to leave only
// a log line — no durable record that the edit did NOT take effect.
// reloadAllowlist now records an AuditReload row for both outcomes and
// is the single code path behind fsnotify, SIGHUP, and admin.reload.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// nopAudit is a race-safe AuditLog that drops every event; used where a
// concurrent goroutine records and the test does not assert on rows.
type nopAudit struct{}

func (nopAudit) Record(context.Context, domain.AuditEvent) error { return nil }

// recordingAudit captures events under a mutex.
type recordingAudit struct {
	mu     sync.Mutex
	events []domain.AuditEvent
}

func (a *recordingAudit) Record(_ context.Context, e domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *recordingAudit) snapshot() []domain.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]domain.AuditEvent(nil), a.events...)
}

func seedAllowlistFile(t *testing.T) (path string, al *domain.Allowlist) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "allowlist.toml")
	seed := domain.NewAllowlist()
	jid, err := domain.Parse("15551234567@s.whatsapp.net")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	seed.Grant(jid, domain.ActionSend)
	if err := saveAllowlist(path, seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	al, err = loadAllowlist(path)
	if err != nil {
		t.Fatalf("load seed: %v", err)
	}
	return path, al
}

// TestReloadAllowlist_ParseErrorAuditsRefused pins the SF-05 contract:
// a broken edit keeps the previous policy AND records a refused
// AuditReload row whose detail contains no raw parse error text.
func TestReloadAllowlist_ParseErrorAuditsRefused(t *testing.T) {
	path, al := seedAllowlistFile(t)
	var mu sync.RWMutex
	audit := &recordingAudit{}

	if err := os.WriteFile(path, []byte("rules = [ {{ not toml"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	_, err := reloadAllowlist(context.Background(), "watchAllowlist", path, al, &mu, audit, silentLogger())
	if err == nil {
		t.Fatal("want parse error, got nil")
	}

	// Previous policy still enforced.
	if al.Size() != 1 {
		t.Errorf("allowlist size = %d, want 1 (previous policy kept)", al.Size())
	}

	evts := audit.snapshot()
	if len(evts) != 1 {
		t.Fatalf("audit events = %d, want 1", len(evts))
	}
	e := evts[0]
	if e.Action != domain.AuditReload {
		t.Errorf("action = %v, want AuditReload", e.Action)
	}
	if e.Decision != "refused" {
		t.Errorf("decision = %q, want refused", e.Decision)
	}
	if e.Source != "watchAllowlist" {
		t.Errorf("source = %q, want watchAllowlist", e.Source)
	}
	// Audit-log hygiene: constant detail, no raw parse error fragments.
	for _, leak := range []string{"toml", "{{", err.Error()} {
		if leak != "" && strings.Contains(e.Detail, leak) {
			t.Errorf("detail %q leaks parse error fragment %q", e.Detail, leak)
		}
	}
}

// TestReloadAllowlist_SuccessSwapsAndAuditsGranted — the happy path
// swaps entries in place and records a granted row.
func TestReloadAllowlist_SuccessSwapsAndAuditsGranted(t *testing.T) {
	path, al := seedAllowlistFile(t)
	var mu sync.RWMutex
	audit := &recordingAudit{}

	// New file: two entries replacing the single seeded one.
	next := domain.NewAllowlist()
	for _, s := range []string{"15550000001@s.whatsapp.net", "15550000002@s.whatsapp.net"} {
		jid, err := domain.Parse(s)
		if err != nil {
			t.Fatalf("parse jid: %v", err)
		}
		next.Grant(jid, domain.ActionSend)
	}
	if err := saveAllowlist(path, next); err != nil {
		t.Fatalf("save next: %v", err)
	}

	entries, err := reloadAllowlist(context.Background(), "internal", path, al, &mu, audit, silentLogger())
	if err != nil {
		t.Fatalf("reloadAllowlist: %v", err)
	}
	if entries != 2 || al.Size() != 2 {
		t.Errorf("entries = %d, al.Size() = %d, want 2/2", entries, al.Size())
	}

	evts := audit.snapshot()
	if len(evts) != 1 {
		t.Fatalf("audit events = %d, want 1", len(evts))
	}
	if evts[0].Decision != "granted" || evts[0].Action != domain.AuditReload {
		t.Errorf("got (%v, %q), want (AuditReload, granted)", evts[0].Action, evts[0].Decision)
	}
}
