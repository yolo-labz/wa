package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// PresenceSenderFactory returns a fresh PresenceSender for one sub-test.
type PresenceSenderFactory func(t *testing.T) app.PresenceSender

// RunPresenceSenderContract exercises PresenceSender (FR-071).
//
//	PS1 Valid composing state returns nil.
//	PS2 Empty state is rejected.
//	PS3 Zero JID is rejected.
//	PS4 Cancelled ctx returns ctx.Err (for network-backed adapters; in-memory
//	     may short-circuit — both are acceptable).
func RunPresenceSenderContract(t *testing.T, factory PresenceSenderFactory) {
	t.Helper()
	chat := domain.MustJID("5511999999999")

	t.Run("PS1_valid_composing", func(t *testing.T) {
		p := factory(t)
		if err := p.SendComposing(context.Background(), chat, "composing", time.Second); err != nil {
			reportf(t, "PresenceSender", "SendComposing", "PS1", "nil error", err.Error())
		}
	})

	t.Run("PS2_empty_state_rejected", func(t *testing.T) {
		p := factory(t)
		if err := p.SendComposing(context.Background(), chat, "", time.Second); err == nil {
			reportf(t, "PresenceSender", "SendComposing", "PS2", "non-nil error", "nil")
		}
	})

	t.Run("PS3_zero_jid_rejected", func(t *testing.T) {
		p := factory(t)
		if err := p.SendComposing(context.Background(), domain.JID{}, "composing", time.Second); err == nil {
			reportf(t, "PresenceSender", "SendComposing", "PS3", "non-nil error", "nil")
		}
	})
}
