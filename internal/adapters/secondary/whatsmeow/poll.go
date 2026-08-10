package whatsmeow

import (
	"context"
	"errors"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// PollManagerAdapter is the whatsmeow-backed implementation of
// app.PollManager (FR-032). Receive-side poll events are already wrapped
// by Tier 1 (poll.question + poll.options[]).
//
// Create is live: whatsmeow's BuildPollCreation (msgsecret.go:342) needs
// only the question and option names, so the adapter can build and send
// the message like any other outbound.
//
// Vote returns domain.ErrUpstreamError by contract, so the socket layer
// surfaces -32000. The constraint is the port shape, not a missing
// helper: BuildPollVote (msgsecret.go:331) needs the poll's full
// types.MessageInfo — chat, SENDER and ID — plus the option NAMES,
// because the wire format hashes names (HashPollOptions) rather than
// carrying indices. The port has chat + pollID + indices, which cannot
// be resolved to a sender or to names without a message-store lookup.
// Widening the port is its own slice.
type PollManagerAdapter struct {
	adapter *Adapter
}

// NewPollManagerFor wires a PollManagerAdapter onto the Adapter's client.
func (a *Adapter) NewPollManagerFor() (*PollManagerAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewPollManagerFor: nil adapter/client")
	}
	return &PollManagerAdapter{adapter: a}, nil
}

// Create implements app.PollManager. Builds a poll-creation message via
// whatsmeow and sends it on the caller's context, returning the sent
// message's ID.
func (p *PollManagerAdapter) Create(ctx context.Context, chat domain.JID, question string, options []string, selectable int) (domain.MessageID, error) {
	const wrap = "PollManagerAdapter.Create"
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p == nil || p.adapter == nil || p.adapter.client == nil {
		return "", fmt.Errorf("%s: %w: nil client", wrap, domain.ErrUpstreamError)
	}
	if chat.IsZero() {
		return "", fmt.Errorf("%s: %w", wrap, domain.ErrInvalidJID)
	}
	if p.adapter.closed.Load() || !p.adapter.client.IsConnected() {
		return "", fmt.Errorf("%s: %w", wrap, domain.ErrDisconnected)
	}

	msg := p.adapter.client.BuildPollCreation(question, options, selectable)
	resp, err := p.adapter.client.SendMessage(ctx, toWhatsmeow(chat), msg)
	if err != nil {
		p.adapter.recordAuditDetail(domain.AuditSend, chat, "error", err.Error())
		return "", fmt.Errorf("%s: %w", wrap, err)
	}
	p.adapter.recordAuditDetail(domain.AuditSend, chat, "ok", resp.ID)
	return domain.MessageID(resp.ID), nil
}

// Vote implements app.PollManager. Every well-formed call returns
// domain.ErrUpstreamError; shape errors are rejected first so the wire
// code stays accurate. See the type comment for why the port shape, not
// upstream, is the constraint.
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
	_ = selected // part of the port signature; unused until it carries names
	return fmt.Errorf("PollManagerAdapter.Vote: %w: poll votes need the poll's sender and option names, which the port does not carry", domain.ErrUpstreamError)
}
