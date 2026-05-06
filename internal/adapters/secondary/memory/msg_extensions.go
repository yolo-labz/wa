package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ErrZeroSourceID is returned by SendForward when sourceID is empty (FS2).
var ErrZeroSourceID = errors.New("memory: zero source message id")

// ErrInvalidDisappearing is returned by SetDisappearing when seconds is
// outside the four-value enum {0, 86400, 604800, 7776000}.
var ErrInvalidDisappearing = errors.New("memory: invalid disappearing seconds")

// ForwardLink is the test-visible view of a SendForward call.
type ForwardLink struct {
	ForwardID  domain.MessageID
	To         domain.JID
	SourceChat domain.JID
	SourceID   domain.MessageID
}

// StarSlot is the test-visible view of the starred state for one message.
type StarSlot struct {
	Chat    domain.JID
	ID      domain.MessageID
	Starred bool
}

// DisappearingSlot is the test-visible view of the disappearing-timer
// configuration for one chat.
type DisappearingSlot struct {
	Chat    domain.JID
	Seconds int
}

// SendForward implements app.ForwardSender. Refuses a zero sourceID
// (contract FS2) and records the forward on a parallel slice so the Sent()
// view stays stable for legacy tests.
func (a *Adapter) SendForward(ctx context.Context, to domain.JID, sourceChat domain.JID, sourceID domain.MessageID) (domain.MessageID, error) {
	if sourceID == "" {
		return "", fmt.Errorf("ForwardSender.SendForward: %w", ErrZeroSourceID)
	}
	if to.IsZero() {
		return "", fmt.Errorf("ForwardSender.SendForward: %w", domain.ErrInvalidJID)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sentSeq++
	id := domain.MessageID("mem-fwd-" + strconv.Itoa(a.sentSeq))
	a.forwards = append(a.forwards, ForwardLink{
		ForwardID:  id,
		To:         to,
		SourceChat: sourceChat,
		SourceID:   sourceID,
	})
	// Also fake a synthesised outbound Message so Sent() reflects the call.
	a.sent = append(a.sent, domain.TextMessage{Recipient: to, Body: "[forwarded]"})
	return id, nil
}

// Forwards returns a snapshot of forward-links recorded by SendForward.
// Test-only accessor.
func (a *Adapter) Forwards() []ForwardLink {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ForwardLink, len(a.forwards))
	copy(out, a.forwards)
	return out
}

// Star implements app.StarSender. Idempotent: starring an already-starred
// message returns nil without emitting a duplicate slot.
func (a *Adapter) Star(ctx context.Context, chat domain.JID, id domain.MessageID, starred bool) error {
	if chat.IsZero() {
		return fmt.Errorf("StarSender.Star: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("StarSender.Star: empty message id")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := chat.String() + ":" + string(id)
	a.starred[key] = StarSlot{Chat: chat, ID: id, Starred: starred}
	return nil
}

// Stars returns a snapshot of every starred-state entry.
// Test-only accessor.
func (a *Adapter) Stars() []StarSlot {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]StarSlot, 0, len(a.starred))
	for _, s := range a.starred {
		out = append(out, s)
	}
	return out
}

// SetDisappearing implements app.DisappearingSetter. Accepts only the four
// WhatsApp-legal durations {0, 86400, 604800, 7776000}; any other value
// returns ErrInvalidDisappearing.
func (a *Adapter) SetDisappearing(ctx context.Context, chat domain.JID, seconds int) error {
	if chat.IsZero() {
		return fmt.Errorf("DisappearingSetter.SetDisappearing: %w", domain.ErrInvalidJID)
	}
	switch seconds {
	case 0, 86_400, 604_800, 7_776_000:
	default:
		return fmt.Errorf("DisappearingSetter.SetDisappearing: %w: %d", ErrInvalidDisappearing, seconds)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.disappearing[chat] = seconds
	return nil
}

// DisappearingFor returns the configured timer for chat, or 0 when unset.
// Test-only accessor.
func (a *Adapter) DisappearingFor(chat domain.JID) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.disappearing[chat]
}

// Disappearings returns a snapshot of every per-chat disappearing-timer
// entry. Test-only accessor.
func (a *Adapter) Disappearings() []DisappearingSlot {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]DisappearingSlot, 0, len(a.disappearing))
	for chat, s := range a.disappearing {
		out = append(out, DisappearingSlot{Chat: chat, Seconds: s})
	}
	return out
}
