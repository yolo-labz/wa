package whatsmeow

import (
	"context"
	"errors"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// PollManagerAdapter is the whatsmeow-backed implementation of
// app.PollManager (FR-032). Receive-side poll events are already wrapped
// by Tier 1 (poll.question + poll.options[]); the outbound Vote path
// stays unimplemented in v2.0.0 because the pinned whatsmeow commit does
// not yet expose a Vote helper. The adapter therefore returns
// domain.ErrUpstreamError from every well-formed call so the socket
// layer surfaces -32000 upstream_error on the wire. When upstream lands
// the helper, replace the body with the real IQ call and the contract
// tests (PM1..PM4) become live.
type PollManagerAdapter struct{}

// NewPollManagerFor wires a PollManagerAdapter onto the Adapter's client.
// Takes no state today — the struct is empty — but follows the
// factory-on-Adapter pattern so the composition root does not need to
// know the adapter carries nothing.
func (a *Adapter) NewPollManagerFor() (*PollManagerAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewPollManagerFor: nil adapter/client")
	}
	return &PollManagerAdapter{}, nil
}

// Vote implements app.PollManager. Every well-formed call returns
// domain.ErrUpstreamError to signal upstream_unsupported; shape errors
// are rejected first so the wire code stays accurate.
func (p *PollManagerAdapter) Vote(ctx context.Context, chat domain.JID, pollID domain.MessageID, selected []int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("PollManagerAdapter.Vote: %w", domain.ErrInvalidJID)
	}
	if pollID == "" {
		return fmt.Errorf("PollManagerAdapter.Vote: %w: empty poll id", domain.ErrInvalidJID)
	}
	_ = selected // retained for the future live impl
	return fmt.Errorf("PollManagerAdapter.Vote: %w: outbound poll votes not yet supported by whatsmeow", domain.ErrUpstreamError)
}
