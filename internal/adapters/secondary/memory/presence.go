package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// composingMinInterval enforces the FR-071 1/s/chat budget. A call made
// within this window of the previous accepted call is silently dropped.
const composingMinInterval = time.Second

// composingSlot records the last accepted composing emission + total
// counters for test assertions.
type composingSlot struct {
	LastAt   time.Time
	Accepted int
	Dropped  int
	LastState string
}

// ComposingEmission is the test-visible view of the presence-per-chat state.
type ComposingEmission struct {
	Chat      domain.JID
	LastAt    time.Time
	Accepted  int
	Dropped   int
	LastState string
}

// SendComposing implements app.PresenceSender. It enforces the 1/s/chat
// rate limit using the adapter clock; excess calls return nil (no error)
// per the port contract and are recorded on the dropped counter for
// TestComposingRateLimit1Hz.
func (a *Adapter) SendComposing(ctx context.Context, chat domain.JID, state string, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("PresenceSender.SendComposing: %w", domain.ErrInvalidJID)
	}
	if state == "" {
		return fmt.Errorf("PresenceSender.SendComposing: empty state")
	}
	now := a.clock.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	slot := a.composing[chat]
	if !slot.LastAt.IsZero() && now.Sub(slot.LastAt) < composingMinInterval {
		slot.Dropped++
		a.composing[chat] = slot
		return nil
	}
	slot.LastAt = now
	slot.Accepted++
	slot.LastState = state
	a.composing[chat] = slot
	return nil
}

// ComposingEmissions returns a snapshot of per-chat composing counters.
// Test-only accessor.
func (a *Adapter) ComposingEmissions() []ComposingEmission {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ComposingEmission, 0, len(a.composing))
	for chat, s := range a.composing {
		out = append(out, ComposingEmission{
			Chat:      chat,
			LastAt:    s.LastAt,
			Accepted:  s.Accepted,
			Dropped:   s.Dropped,
			LastState: s.LastState,
		})
	}
	return out
}
