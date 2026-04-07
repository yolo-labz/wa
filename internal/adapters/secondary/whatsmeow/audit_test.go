package whatsmeow

import (
	"context"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

func mkEvent(detail string) domain.AuditEvent {
	return domain.NewAuditEvent("test", domain.AuditSend, domain.MustJID("5511999990000@s.whatsapp.net"), "allow", detail)
}

func TestAuditRing_RecordAndSnapshot(t *testing.T) {
	t.Parallel()
	r := newAuditRing(5)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := r.Record(ctx, mkEvent("e")); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Errorf("len = %d, want 3", len(snap))
	}
	if r.Len() != 3 {
		t.Errorf("Len = %d, want 3", r.Len())
	}
}

func TestAuditRing_WrapAround(t *testing.T) {
	t.Parallel()
	r := newAuditRing(3)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		_ = r.Record(ctx, mkEvent(string(rune('a'+i))))
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d, want 3", len(snap))
	}
	// Oldest retained = 'e' (index 4), newest = 'g' (index 6).
	if snap[0].Detail != "e" || snap[1].Detail != "f" || snap[2].Detail != "g" {
		t.Errorf("wrap order wrong: %q %q %q", snap[0].Detail, snap[1].Detail, snap[2].Detail)
	}
	if r.Len() != 3 {
		t.Errorf("Len = %d, want 3", r.Len())
	}
}

func TestAuditRing_SnapshotDefensiveCopy(t *testing.T) {
	t.Parallel()
	r := newAuditRing(2)
	_ = r.Record(context.Background(), mkEvent("a"))
	snap := r.Snapshot()
	snap[0].Detail = "mutated"
	snap2 := r.Snapshot()
	if snap2[0].Detail != "a" {
		t.Errorf("snapshot is not defensive: %q", snap2[0].Detail)
	}
}

func TestAuditRing_ParallelRecord(t *testing.T) {
	t.Parallel()
	r := newAuditRing(1000)
	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = r.Record(ctx, mkEvent("p"))
			}
		}()
	}
	wg.Wait()
	if r.Len() != 1000 {
		t.Errorf("Len = %d, want 1000", r.Len())
	}
}

func TestNewAuditRing_PanicsOnZeroCap(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = newAuditRing(0)
}
