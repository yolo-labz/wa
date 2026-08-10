package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ErrPollUpstreamUnsupported is the typed error the PollManager fake
// returns from Vote. It mirrors the whatsmeow adapter behaviour in
// v2.0.0 (FR-032, tasks-tier2 T2-20) where outbound vote sends are not
// yet supported upstream.
var ErrPollUpstreamUnsupported = errors.New("memory: poll.vote upstream_unsupported")

// PollManager is the in-memory implementation of app.PollManager.
// Create records the poll and returns a synthetic id; Vote returns
// ErrPollUpstreamUnsupported so callers can exercise the error-routing
// path without a real WhatsApp helper.
type PollManager struct {
	mu      sync.Mutex
	created []CreatedPoll
}

// CreatedPoll is one recorded poll.create call.
type CreatedPoll struct {
	Chat       domain.JID
	Question   string
	Options    []string
	Selectable int
	ID         domain.MessageID
}

// NewPollManager returns the stub.
func NewPollManager() *PollManager { return &PollManager{} }

// Created returns a copy of the recorded polls, newest last.
func (p *PollManager) Created() []CreatedPoll {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.created)
}

// Create implements app.PollManager. Options are cloned so a caller
// mutating its slice afterwards cannot rewrite recorded history.
func (p *PollManager) Create(ctx context.Context, chat domain.JID, question string, options []string, selectable int) (domain.MessageID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if chat.IsZero() {
		return "", fmt.Errorf("PollManager.Create: %w", domain.ErrInvalidJID)
	}
	if question == "" || len(options) < 2 {
		return "", errors.New("PollManager.Create: question and >=2 options required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := domain.MessageID(fmt.Sprintf("mem-poll-%d", len(p.created)+1))
	p.created = append(p.created, CreatedPoll{
		Chat: chat, Question: question, Options: slices.Clone(options),
		Selectable: selectable, ID: id,
	})
	return id, nil
}

// Vote implements app.PollManager.
//
// Its entry guards are byte-identical to PollManagerAdapter.Vote in the
// whatsmeow adapter, and that is the point: both implement the same port, so
// both owe the caller the same refusal for the same input. Hoisting them into
// a shared helper would make this in-memory fake depend on code shared with
// the real adapter, which is the coupling the hexagonal split exists to
// prevent — a fake that drifts with its production twin cannot falsify it.
// Five duplicated lines is the cheaper side of that trade, so the duplication
// is declared to jscpd rather than engineered away.
func (*PollManager) Vote(ctx context.Context, chat domain.JID, pollID domain.MessageID, selected []int) error {
	// jscpd:ignore-start
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("PollManager.Vote: %w", domain.ErrInvalidJID)
	}
	// jscpd:ignore-end
	if pollID == "" {
		return errors.New("PollManager.Vote: empty poll id")
	}
	return ErrPollUpstreamUnsupported
}
