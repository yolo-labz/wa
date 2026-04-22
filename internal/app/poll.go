package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// pollVoteParams is the JSON-RPC params for "poll.vote" (FR-032).
// Selected is the set of option indices the caller wants to vote for;
// an empty slice is permitted by whatsmeow for vote-clearing semantics.
type pollVoteParams struct {
	Chat     string `json:"chat"`
	PollID   string `json:"pollId"`
	Selected []int  `json:"selected"`
}

// handlePollVote implements "poll.vote" (FR-032). Idempotency-wrapped
// because a replay of the same vote must not emit a second upstream
// attempt. In v2.0.0 the adapter returns -32000 upstream_error for any
// well-formed call; shape errors come back as -32602 invalid_params.
func (d *Dispatcher) handlePollVote(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "poll.vote", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doPollVote(ctx, raw)
	})
}

func (d *Dispatcher) doPollVote(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.polls == nil {
		return nil, ErrMethodNotFound
	}
	var p pollVoteParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" || p.PollID == "" {
		return nil, ErrInvalidParams
	}
	chat, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.polls.Vote(ctx, chat, domain.MessageID(p.PollID), p.Selected); err != nil {
		return nil, fmt.Errorf("poll.vote: %w", err)
	}
	return json.Marshal(struct{}{})
}
