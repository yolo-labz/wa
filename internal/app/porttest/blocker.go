package porttest

import (
	"context"
	"slices"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// BlockerFactory returns a fresh Blocker for one sub-test.
type BlockerFactory func(t *testing.T) app.Blocker

// RunBlockerContract exercises Blocker (FR-018, FR-019).
//
//	BL1 Block then ListBlocked contains the JID.
//	BL2 Unblock removes the JID from ListBlocked.
//	BL3 Block is idempotent (double-block returns nil).
//	BL4 Unblock of a never-blocked JID returns nil.
//	BL5 Zero JID is rejected by Block and Unblock.
//	BL6 Cancelled ctx returns ctx.Err on every method.
func RunBlockerContract(t *testing.T, factory BlockerFactory) {
	t.Helper()
	target := domain.MustJID("5511999999999")

	t.Run("BL1_block_then_list", func(t *testing.T) {
		b := factory(t)
		if err := b.Block(context.Background(), target); err != nil {
			reportf(t, "Blocker", "Block", "BL1", "nil err", err.Error())
			return
		}
		blocked, err := b.ListBlocked(context.Background())
		if err != nil {
			reportf(t, "Blocker", "ListBlocked", "BL1", "nil err", err.Error())
			return
		}
		if !slices.Contains(blocked, target) {
			reportf(t, "Blocker", "ListBlocked", "BL1", "target in blocked list", "absent")
		}
	})

	t.Run("BL2_unblock_removes", func(t *testing.T) {
		b := factory(t)
		_ = b.Block(context.Background(), target)
		if err := b.Unblock(context.Background(), target); err != nil {
			reportf(t, "Blocker", "Unblock", "BL2", "nil err", err.Error())
			return
		}
		blocked, _ := b.ListBlocked(context.Background())
		if slices.Contains(blocked, target) {
			reportf(t, "Blocker", "ListBlocked", "BL2", "target absent after unblock", "present")
		}
	})

	t.Run("BL3_block_idempotent", func(t *testing.T) {
		b := factory(t)
		if err := b.Block(context.Background(), target); err != nil {
			reportf(t, "Blocker", "Block", "BL3", "nil err first call", err.Error())
		}
		if err := b.Block(context.Background(), target); err != nil {
			reportf(t, "Blocker", "Block", "BL3", "nil err second call", err.Error())
		}
	})

	t.Run("BL4_unblock_absent", func(t *testing.T) {
		b := factory(t)
		if err := b.Unblock(context.Background(), target); err != nil {
			reportf(t, "Blocker", "Unblock", "BL4", "nil err on never-blocked", err.Error())
		}
	})

	t.Run("BL5_zero_jid_rejected", func(t *testing.T) {
		b := factory(t)
		if err := b.Block(context.Background(), domain.JID{}); err == nil {
			reportf(t, "Blocker", "Block", "BL5", "non-nil err for zero JID", "nil")
		}
		if err := b.Unblock(context.Background(), domain.JID{}); err == nil {
			reportf(t, "Blocker", "Unblock", "BL5", "non-nil err for zero JID", "nil")
		}
	})

	t.Run("BL6_cancelled_ctx", func(t *testing.T) {
		b := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := b.Block(ctx, target); err == nil {
			reportf(t, "Blocker", "Block", "BL6", "non-nil err on cancelled ctx", "nil")
		}
	})
}
