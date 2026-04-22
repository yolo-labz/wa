package whatsmeow

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// fillEventCh pushes cap(a.eventCh) direct-channel sends so the next
// enqueueBounded attempt MUST time out. Direct channel sends bypass
// eventRing, so the ring keeps its own history — exactly the state the
// R-04 tests need.
func fillEventCh(t *testing.T, a *Adapter) {
	t.Helper()
	for i := 0; i < cap(a.eventCh); i++ {
		select {
		case a.eventCh <- domain.ConnectionEvent{
			ID:    domain.EventID("filler"),
			TS:    fixedNow,
			State: domain.ConnConnected,
		}:
		default:
			t.Fatalf("eventCh refused filler push at i=%d", i)
		}
	}
}

// TestBackpressureEmitsStreamDrop: when the 100ms send budget expires
// because eventCh is full, the adapter MUST record an AuditStreamDrop
// entry. We do NOT assert the synthetic StreamDropEvent reaches the
// consumer here (the channel is at capacity — the drop is best-effort).
// The audit row is the non-negotiable signal.
func TestBackpressureEmitsStreamDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fc := newFakeClient()
		a := newTestAdapter(t, fc)
		t.Cleanup(func() { _ = a.Close() })

		fillEventCh(t, a)

		dropped := domain.ConnectionEvent{
			ID:    "dropped-99",
			TS:    fixedNow,
			State: domain.ConnDisconnected,
		}
		done := make(chan bool, 1)
		go func() { done <- a.enqueueBounded(99, dropped) }()

		// Advance past the 100 ms budget. synctest stops virtual time
		// until every goroutine is blocked, then we can tick it.
		time.Sleep(streamSendTimeout + 10*time.Millisecond)
		ok := <-done
		if ok {
			t.Fatalf("enqueueBounded returned true despite full buffer")
		}

		// Assert an AuditStreamDrop row exists with decision=buffer_full.
		entries := a.auditBuf.Snapshot()
		var found bool
		for _, e := range entries {
			if e.Action == domain.AuditStreamDrop && e.Decision == "buffer_full" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("want AuditStreamDrop[buffer_full] audit entry; got %d entries", len(entries))
			for _, e := range entries {
				t.Logf("  audit: action=%s decision=%s detail=%s", e.Action, e.Decision, e.Detail)
			}
		}
	})
}

// TestResumeFromBufferedSeq: after pushing 3 events with seqs 1,2,3,
// ResumeFrom(1) must replay seqs 2 and 3 via Next() — and no
// StreamDropEvent, because the entire range is in the ring.
func TestResumeFromBufferedSeq(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	// Populate the ring directly (bypass eventCh since Next consumes it).
	for seq := uint64(1); seq <= 3; seq++ {
		a.eventRing.push(seq, domain.ConnectionEvent{
			ID:    domain.EventID([]byte{byte('0' + seq)}),
			TS:    fixedNow,
			State: domain.ConnConnected,
		})
	}

	if err := a.ResumeFrom(1); err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for _, wantID := range []domain.EventID{"2", "3"} {
		got, err := a.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if _, isDrop := got.(domain.StreamDropEvent); isDrop {
			t.Fatalf("unexpected StreamDropEvent, ring covered the range")
		}
		if got.EventID() != wantID {
			t.Errorf("want id=%q; got %q", wantID, got.EventID())
		}
	}
}

// TestResumeFromDropsGapSignalsDrop: after pushing events with seqs
// 10..12 only (simulating a ring that has evicted older entries),
// ResumeFrom(2) MUST prepend a StreamDropEvent covering [3, 9] before
// the replayed 10,11,12 events.
func TestResumeFromDropsGapSignalsDrop(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	for seq := uint64(10); seq <= 12; seq++ {
		a.eventRing.push(seq, domain.ConnectionEvent{
			ID:    domain.EventID([]byte{byte('0' + seq/10), byte('0' + seq%10)}),
			TS:    fixedNow,
			State: domain.ConnConnected,
		})
	}

	if err := a.ResumeFrom(2); err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// First event must be a StreamDropEvent covering [3, 9].
	first, err := a.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	drop, ok := first.(domain.StreamDropEvent)
	if !ok {
		t.Fatalf("want StreamDropEvent first; got %T", first)
	}
	if drop.FromSeq != 3 || drop.ToSeq != 9 || drop.DroppedCount != 7 {
		t.Errorf("drop range want [3,9] count=7; got [%d,%d] count=%d",
			drop.FromSeq, drop.ToSeq, drop.DroppedCount)
	}
	if drop.Reason != "resume_gap" {
		t.Errorf("reason=%q; want resume_gap", drop.Reason)
	}

	// Then the three buffered events in order.
	for _, wantID := range []domain.EventID{"10", "11", "12"} {
		got, err := a.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if got.EventID() != wantID {
			t.Errorf("want id=%q; got %q", wantID, got.EventID())
		}
	}

	// Confirm the audit row was written.
	entries := a.auditBuf.Snapshot()
	var found bool
	for _, e := range entries {
		if e.Action == domain.AuditStreamDrop && e.Decision == "resume_gap" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("want AuditStreamDrop[resume_gap] audit entry")
	}
}
