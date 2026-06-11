package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// presenceMinInterval enforces the FR-071 1 send/chat/second budget at the
// adapter boundary. A call made within this window of the previous accepted
// call for the same chat is silently dropped (returns nil) per the
// app.PresenceSender contract — the reference semantics live in
// internal/adapters/secondary/memory/presence.go.
const presenceMinInterval = time.Second

// PresenceAdapter is the whatsmeow-backed implementation of
// app.PresenceSender (FR-071, roadmap 2.3 --humanize). It maps the port's
// tri-state strings onto WhatsApp's two-tag wire model: "composing" and
// "paused" are first-class tags; "recording" is composing with
// media="audio".
type PresenceAdapter struct {
	client whatsmeowClient
	logger *slog.Logger
	nowFn  func() time.Time

	mu     sync.Mutex
	lastAt map[domain.JID]time.Time
}

// NewPresenceFor wires a PresenceAdapter to the Adapter's whatsmeow client
// + clock. Returns an error only if the client is nil (the composition root
// surfaces that as a startup degradation rather than a nil-pointer panic
// later).
func (a *Adapter) NewPresenceFor() (*PresenceAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewPresenceFor: nil adapter/client")
	}
	return &PresenceAdapter{
		client: a.client,
		logger: a.logger,
		nowFn:  a.nowFn,
		lastAt: make(map[domain.JID]time.Time),
	}, nil
}

// SendComposing implements app.PresenceSender. duration is advisory and
// ignored — WhatsApp auto-expires a composing indicator server-side (~10 s)
// and the dispatcher's stop methods emit the explicit "paused" frame.
//
// The 1/s/chat budget is consumed BEFORE the wire call so a failing
// upstream cannot be hammered at request rate; a budget-dropped call
// returns nil without contacting the network, matching the port contract.
func (p *PresenceAdapter) SendComposing(ctx context.Context, chat domain.JID, state string, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("PresenceAdapter.SendComposing: %w", domain.ErrInvalidJID)
	}

	wireState := waTypes.ChatPresenceComposing
	media := waTypes.ChatPresenceMediaText
	switch state {
	case "composing":
	case "recording":
		media = waTypes.ChatPresenceMediaAudio
	case "paused":
		wireState = waTypes.ChatPresencePaused
	default:
		return fmt.Errorf("PresenceAdapter.SendComposing: unknown state %q", state)
	}

	if !p.client.IsConnected() {
		return fmt.Errorf("PresenceAdapter.SendComposing: %w", domain.ErrDisconnected)
	}

	now := p.nowFn()
	p.mu.Lock()
	if last, ok := p.lastAt[chat]; ok && now.Sub(last) < presenceMinInterval {
		p.mu.Unlock()
		return nil
	}
	p.lastAt[chat] = now
	p.mu.Unlock()

	if err := p.client.SendChatPresence(ctx, toWhatsmeow(chat), wireState, media); err != nil {
		return fmt.Errorf("PresenceAdapter.SendComposing: %w", err)
	}
	return nil
}
