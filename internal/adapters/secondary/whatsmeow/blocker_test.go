package whatsmeow

import (
	"context"
	"testing"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/app/porttest"
	"github.com/yolo-labz/wa/internal/domain"
)

// TestBlockerAdapter_SatisfiesContract drives the Blocker contract
// (BL1..BL6) against the whatsmeow adapter with a fake client.
func TestBlockerAdapter_SatisfiesContract(t *testing.T) {
	porttest.RunBlockerContract(t, func(t *testing.T) app.Blocker {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
		t.Cleanup(func() { _ = a.Close() })
		b, err := a.NewBlockerFor()
		if err != nil {
			t.Fatalf("NewBlockerFor: %v", err)
		}
		return b
	})
}

func newTestBlocker(t *testing.T) (*BlockerAdapter, *fakeWhatsmeowClient, *Adapter) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
	t.Cleanup(func() { _ = a.Close() })
	b, err := a.NewBlockerFor()
	if err != nil {
		t.Fatalf("NewBlockerFor: %v", err)
	}
	return b, fc, a
}

// TestBlockWritesAudit verifies that Block and Unblock each write a matching
// audit entry on success. Required by T2-09.
func TestBlockWritesAudit(t *testing.T) {
	b, _, a := newTestBlocker(t)
	target := domain.MustJID("5511999999999")
	ctx := context.Background()

	if err := b.Block(ctx, target); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if err := b.Unblock(ctx, target); err != nil {
		t.Fatalf("Unblock: %v", err)
	}

	snap := a.auditBuf.Snapshot()
	var sawBlock, sawUnblock bool
	for _, e := range snap {
		if e.Subject != target {
			continue
		}
		if e.Action == domain.AuditBlock && e.Decision == "ok" {
			sawBlock = true
		}
		if e.Action == domain.AuditUnblock && e.Decision == "ok" {
			sawUnblock = true
		}
	}
	if !sawBlock {
		t.Errorf("missing AuditBlock entry for %s; snap=%+v", target, snap)
	}
	if !sawUnblock {
		t.Errorf("missing AuditUnblock entry for %s; snap=%+v", target, snap)
	}
}

// TestListBlockedFromServer verifies that ListBlocked reads GetBlocklist live
// — no local cache — so an out-of-band (phone UI) unblock is reflected on
// the next call. Required by T2-09.
func TestListBlockedFromServer(t *testing.T) {
	b, fc, _ := newTestBlocker(t)
	ctx := context.Background()

	a := waTypes.NewJID("5511111111111", waTypes.DefaultUserServer)
	bJID := waTypes.NewJID("5511222222222", waTypes.DefaultUserServer)

	fc.mu.Lock()
	fc.BlocklistJIDs = []waTypes.JID{a, bJID}
	fc.mu.Unlock()

	got, err := b.ListBlocked(ctx)
	if err != nil {
		t.Fatalf("ListBlocked #1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 blocked, got %d: %+v", len(got), got)
	}

	// Simulate out-of-band unblock on the server: shrink the seed list.
	fc.mu.Lock()
	fc.BlocklistJIDs = []waTypes.JID{a}
	fc.mu.Unlock()

	got2, err := b.ListBlocked(ctx)
	if err != nil {
		t.Fatalf("ListBlocked #2: %v", err)
	}
	if len(got2) != 1 {
		t.Fatalf("want 1 blocked after out-of-band unblock, got %d: %+v", len(got2), got2)
	}
	if got2[0].String() != "5511111111111@s.whatsapp.net" {
		t.Fatalf("unexpected remaining JID: %s", got2[0])
	}
}

// TestBlockInvokesUpdateBlocklist verifies the adapter drives the exact
// waEvents.BlocklistChangeAction on the wire for Block vs Unblock.
func TestBlockInvokesUpdateBlocklist(t *testing.T) {
	b, fc, _ := newTestBlocker(t)
	target := domain.MustJID("5511999999999")
	ctx := context.Background()

	if err := b.Block(ctx, target); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if err := b.Unblock(ctx, target); err != nil {
		t.Fatalf("Unblock: %v", err)
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.BlocklistUpdates) != 2 {
		t.Fatalf("want 2 UpdateBlocklist calls, got %d", len(fc.BlocklistUpdates))
	}
	if fc.BlocklistUpdates[0].Action != waEvents.BlocklistChangeActionBlock {
		t.Errorf("first call action = %v, want Block", fc.BlocklistUpdates[0].Action)
	}
	if fc.BlocklistUpdates[1].Action != waEvents.BlocklistChangeActionUnblock {
		t.Errorf("second call action = %v, want Unblock", fc.BlocklistUpdates[1].Action)
	}
}
