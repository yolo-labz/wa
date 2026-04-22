package porttest

import (
	"context"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// MessageModeratorFactory returns a fresh MessageModerator for one sub-test.
type MessageModeratorFactory func(t *testing.T) app.MessageModerator

// RunMessageModeratorContract exercises MessageModerator (FR-014, FR-015,
// FR-015a).
//
//	MM1 Revoke(self) succeeds without network I/O (contract shape only).
//	MM2 Revoke(everyone) succeeds.
//	MM3 Revoke rejects an invalid RevokeScope (zero value).
//	MM4 Edit happy path returns nil.
//	MM5 Edit rejects newBody that exceeds domain.MaxTextBytes.
//	MM6 Cancelled ctx returns ctx.Err on both Revoke and Edit.
func RunMessageModeratorContract(t *testing.T, factory MessageModeratorFactory) {
	t.Helper()
	chat := domain.MustJID("5511999999999")
	msgID := domain.MessageID("3EB0C1D2E3F4A5B6C7D8")

	t.Run("MM1_revoke_self", func(t *testing.T) {
		m := factory(t)
		if err := m.Revoke(context.Background(), chat, msgID, domain.RevokeSelf); err != nil {
			reportf(t, "MessageModerator", "Revoke", "MM1", "nil err for scope=self", err.Error())
		}
	})

	t.Run("MM2_revoke_everyone", func(t *testing.T) {
		m := factory(t)
		if err := m.Revoke(context.Background(), chat, msgID, domain.RevokeEveryone); err != nil {
			reportf(t, "MessageModerator", "Revoke", "MM2", "nil err for scope=everyone", err.Error())
		}
	})

	t.Run("MM3_revoke_invalid_scope", func(t *testing.T) {
		m := factory(t)
		if err := m.Revoke(context.Background(), chat, msgID, domain.RevokeScope(0)); err == nil {
			reportf(t, "MessageModerator", "Revoke", "MM3", "non-nil err for zero scope", "nil")
		}
	})

	t.Run("MM4_edit_ok", func(t *testing.T) {
		m := factory(t)
		if err := m.Edit(context.Background(), chat, msgID, "corrected body"); err != nil {
			reportf(t, "MessageModerator", "Edit", "MM4", "nil err", err.Error())
		}
	})

	t.Run("MM5_edit_oversize_rejected", func(t *testing.T) {
		m := factory(t)
		tooBig := strings.Repeat("x", domain.MaxTextBytes+1)
		if err := m.Edit(context.Background(), chat, msgID, tooBig); err == nil {
			reportf(t, "MessageModerator", "Edit", "MM5", "non-nil err for oversize body", "nil")
		}
	})

	t.Run("MM6_cancelled_ctx_revoke", func(t *testing.T) {
		m := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := m.Revoke(ctx, chat, msgID, domain.RevokeSelf); err == nil {
			reportf(t, "MessageModerator", "Revoke", "MM6", "non-nil err on cancelled ctx", "nil")
		}
	})
}
