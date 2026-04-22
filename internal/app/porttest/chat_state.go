package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// ChatStateManagerFactory returns a fresh ChatStateManager for one sub-test.
type ChatStateManagerFactory func(t *testing.T) app.ChatStateManager

// RunChatStateManagerContract exercises ChatStateManager (FR-016).
//
//	CS1 Archive toggles idempotently (two Archive(true) calls return nil).
//	CS2 Mute with future timestamp returns nil.
//	CS3 Mute with past timestamp returns a non-nil error.
//	CS4 Mute with nil unmutes and returns nil.
//	CS5 Pin toggles.
//	CS6 MarkUnread is idempotent.
//	CS7 Cancelled ctx returns ctx.Err on every method.
func RunChatStateManagerContract(t *testing.T, factory ChatStateManagerFactory) {
	t.Helper()
	chat := domain.MustJID("5511999999999")

	t.Run("CS1_archive_idempotent", func(t *testing.T) {
		m := factory(t)
		if err := m.Archive(context.Background(), chat, true); err != nil {
			reportf(t, "ChatStateManager", "Archive", "CS1", "nil err", err.Error())
		}
		if err := m.Archive(context.Background(), chat, true); err != nil {
			reportf(t, "ChatStateManager", "Archive", "CS1", "nil err on second archive(true)", err.Error())
		}
	})

	t.Run("CS2_mute_future", func(t *testing.T) {
		m := factory(t)
		future := time.Now().Add(time.Hour)
		if err := m.Mute(context.Background(), chat, &future); err != nil {
			reportf(t, "ChatStateManager", "Mute", "CS2", "nil err", err.Error())
		}
	})

	t.Run("CS3_mute_past_rejected", func(t *testing.T) {
		m := factory(t)
		past := time.Now().Add(-time.Hour)
		if err := m.Mute(context.Background(), chat, &past); err == nil {
			reportf(t, "ChatStateManager", "Mute", "CS3", "non-nil err for past timestamp", "nil")
		}
	})

	t.Run("CS4_mute_nil_unmutes", func(t *testing.T) {
		m := factory(t)
		if err := m.Mute(context.Background(), chat, nil); err != nil {
			reportf(t, "ChatStateManager", "Mute", "CS4", "nil err", err.Error())
		}
	})

	t.Run("CS5_pin_toggle", func(t *testing.T) {
		m := factory(t)
		if err := m.Pin(context.Background(), chat, true); err != nil {
			reportf(t, "ChatStateManager", "Pin", "CS5", "nil err", err.Error())
		}
		if err := m.Pin(context.Background(), chat, false); err != nil {
			reportf(t, "ChatStateManager", "Pin", "CS5", "nil err on unpin", err.Error())
		}
	})

	t.Run("CS6_mark_unread_idempotent", func(t *testing.T) {
		m := factory(t)
		if err := m.MarkUnread(context.Background(), chat); err != nil {
			reportf(t, "ChatStateManager", "MarkUnread", "CS6", "nil err", err.Error())
		}
		if err := m.MarkUnread(context.Background(), chat); err != nil {
			reportf(t, "ChatStateManager", "MarkUnread", "CS6", "nil err on repeat", err.Error())
		}
	})

	t.Run("CS7_cancelled_ctx", func(t *testing.T) {
		m := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := m.Archive(ctx, chat, true); err == nil {
			reportf(t, "ChatStateManager", "Archive", "CS7", "non-nil err on cancelled ctx", "nil")
		}
	})
}
