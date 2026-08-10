package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Poll shape limits. WhatsApp's own client caps a poll at 12 options and
// requires at least 2; the question rides in a normal message body, so it
// takes the domain's text limit. Enforced here rather than in the adapter
// so a bad shape costs no round trip.
const (
	pollMinOptions = 2
	pollMaxOptions = 12
)

// pollCreateParams is the JSON-RPC params for "poll.create" (FR-032).
// Selectable is how many options one voter may pick; 0 and 1 both mean
// single-choice, matching whatsmeow's BuildPollCreation.
type pollCreateParams struct {
	Chat       string   `json:"chat"`
	Question   string   `json:"question"`
	Options    []string `json:"options"`
	Selectable int      `json:"selectable,omitempty"`
}

// pollCreateResult carries the sent poll's message id so the caller can
// correlate later poll.update events.
type pollCreateResult struct {
	MessageID string `json:"messageId"`
}

// handlePollCreate implements "poll.create" (FR-032). Idempotency-wrapped
// so a retry cannot post the same poll twice.
func (d *Dispatcher) handlePollCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "poll.create", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doPollCreate(ctx, raw)
	})
}

// validate checks every shape invariant poll.create requires, so the
// handler stays one decision deep. Each branch returns the same
// ErrInvalidParams the wire contract specifies; the split is for
// readability and gocyclo, not for error granularity.
func (p pollCreateParams) validate() error {
	if p.Chat == "" || p.Question == "" {
		return ErrInvalidParams
	}
	if len(p.Options) < pollMinOptions || len(p.Options) > pollMaxOptions {
		return ErrInvalidParams
	}
	if len(p.Question) > domain.MaxTextBytes {
		return ErrInvalidParams
	}
	seen := make(map[string]bool, len(p.Options))
	for _, o := range p.Options {
		// Blank or duplicate options make a poll a voter cannot answer
		// unambiguously; WhatsApp's client refuses them too.
		if o == "" || len(o) > domain.MaxTextBytes || seen[o] {
			return ErrInvalidParams
		}
		seen[o] = true
	}
	if p.Selectable < 0 || p.Selectable > len(p.Options) {
		return ErrInvalidParams
	}
	return nil
}

func (d *Dispatcher) doPollCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.polls == nil {
		return nil, ErrMethodNotFound
	}
	var p pollCreateParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	chat, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	id, err := d.polls.Create(ctx, chat, p.Question, p.Options, p.Selectable)
	if err != nil {
		return nil, fmt.Errorf("poll.create: %w", err)
	}
	return json.Marshal(pollCreateResult{MessageID: string(id)})
}

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
