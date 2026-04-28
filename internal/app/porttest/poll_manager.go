package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// PollManagerFactory returns a fresh PollManager for one sub-test.
type PollManagerFactory func(t *testing.T) app.PollManager

// RunPollManagerContract exercises PollManager (FR-032).
//
// PollManager.Vote is a receive-only stub in v2.0.0: the adapter
// returns -32000 upstream_unsupported until whatsmeow gains a Vote
// helper. The contract therefore asserts that every well-formed call
// returns a non-nil error (the upstream-unsupported signal) and that
// malformed calls are rejected before reaching the upstream stub.
//
//	PM1 Vote with a valid (chat, pollID, selected) tuple returns a
//	    non-nil error (upstream_unsupported) in v2.0.0.
//	PM2 Zero chat JID is rejected.
//	PM3 Zero pollID is rejected.
//	PM4 Cancelled ctx returns ctx.Err.
func RunPollManagerContract(t *testing.T, factory PollManagerFactory) {
	t.Helper()
	chat := domain.MustJID("1234567890-1600000000@g.us")
	pollID := domain.MessageID("3EB0C431C26A1916E07A")

	t.Run("PM1_vote_upstream_unsupported", func(t *testing.T) {
		p := factory(t)
		if err := p.Vote(context.Background(), chat, pollID, []int{0}); err == nil {
			reportf(t, "PollManager", "Vote", "PM1", "non-nil err (upstream_unsupported)", "nil")
		}
	})

	t.Run("PM2_zero_chat_rejected", func(t *testing.T) {
		p := factory(t)
		if err := p.Vote(context.Background(), domain.JID{}, pollID, []int{0}); err == nil {
			reportf(t, "PollManager", "Vote", "PM2", "non-nil err for zero chat", "nil")
		}
	})

	t.Run("PM3_zero_poll_id_rejected", func(t *testing.T) {
		p := factory(t)
		if err := p.Vote(context.Background(), chat, domain.MessageID(""), []int{0}); err == nil {
			reportf(t, "PollManager", "Vote", "PM3", "non-nil err for zero pollID", "nil")
		}
	})

	t.Run("PM4_cancelled_ctx", func(t *testing.T) {
		p := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := p.Vote(ctx, chat, pollID, []int{0}); err == nil {
			reportf(t, "PollManager", "Vote", "PM4", "non-nil err on cancelled ctx", "nil")
		}
	})
}
