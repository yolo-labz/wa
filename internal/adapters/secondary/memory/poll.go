package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// ErrPollUpstreamUnsupported is the typed error the PollManager fake
// returns from Vote. It mirrors the whatsmeow adapter behaviour in
// v2.0.0 (FR-032, tasks-tier2 T2-20) where outbound vote sends are not
// yet supported upstream.
var ErrPollUpstreamUnsupported = errors.New("memory: poll.vote upstream_unsupported")

// PollManager is the in-memory implementation of app.PollManager. All
// well-formed calls return ErrPollUpstreamUnsupported so callers can
// exercise the error-routing path without a real WhatsApp helper.
type PollManager struct{}

// NewPollManager returns the stub.
func NewPollManager() *PollManager { return &PollManager{} }

// Vote implements app.PollManager.
func (*PollManager) Vote(ctx context.Context, chat domain.JID, pollID domain.MessageID, selected []int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("PollManager.Vote: %w", domain.ErrInvalidJID)
	}
	if pollID == "" {
		return errors.New("PollManager.Vote: empty poll id")
	}
	return ErrPollUpstreamUnsupported
}
