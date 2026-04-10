package slogaudit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/internal/domain"
)

func TestAudit_WriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "wa", "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	events := []domain.AuditEvent{
		{TS: now, Actor: "test", Action: domain.AuditSend, Subject: domain.JID{}, Decision: "ok", Detail: "msg1"},
		{TS: now.Add(time.Millisecond), Actor: "test", Action: domain.AuditGrant, Subject: domain.JID{}, Decision: "ok", Detail: "grant1"},
		{TS: now.Add(2 * time.Millisecond), Actor: "test", Action: domain.AuditPair, Subject: domain.JID{}, Decision: "ok", Detail: "pair1"},
	}

	for _, e := range events {
		if err := a.Record(ctx, e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	// Read the file and verify JSON lines.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines int
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Errorf("line %d: invalid JSON: %v", lines, err)
		}
		// Verify key fields are present.
		for _, key := range []string{"msg", "actor", "action", "decision"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("line %d: missing key %q", lines, key)
			}
		}
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != 3 {
		t.Errorf("expected 3 lines, got %d", lines)
	}
}

func TestAudit_OutOfOrderRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// First event succeeds.
	e1 := domain.AuditEvent{TS: now.Add(time.Second), Actor: "a", Action: domain.AuditSend, Decision: "ok"}
	if err := a.Record(ctx, e1); err != nil {
		t.Fatalf("Record e1: %v", err)
	}

	// Second event with earlier timestamp is rejected.
	e2 := domain.AuditEvent{TS: now, Actor: "b", Action: domain.AuditSend, Decision: "ok"}
	err = a.Record(ctx, e2)
	if err == nil {
		t.Fatal("expected out-of-order error, got nil")
	}
	if !errors.Is(err, slogaudit.ErrOutOfOrder) {
		t.Errorf("expected ErrOutOfOrder, got %v", err)
	}

	// Same timestamp is also rejected (not strictly after).
	e3 := domain.AuditEvent{TS: now.Add(time.Second), Actor: "c", Action: domain.AuditSend, Decision: "ok"}
	err = a.Record(ctx, e3)
	if err == nil {
		t.Fatal("expected out-of-order error for equal TS, got nil")
	}
}

func TestAudit_ConcurrentSafety(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a.Close()

	ctx := context.Background()
	base := time.Now().UTC()

	// Launch concurrent writers with strictly increasing timestamps.
	// Only one goroutine per timestamp, so no duplicates.
	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			e := domain.AuditEvent{
				TS:       base.Add(time.Duration(idx) * time.Millisecond),
				Actor:    "concurrent",
				Action:   domain.AuditSend,
				Decision: "ok",
			}
			errs[idx] = a.Record(ctx, e)
		}(i)
	}
	wg.Wait()

	// Some will succeed, some will be out-of-order — that's correct under
	// concurrency. The important thing is no panic, no data race.
	var successes int
	for _, e := range errs {
		if e == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Error("expected at least one successful write under concurrency")
	}
}
