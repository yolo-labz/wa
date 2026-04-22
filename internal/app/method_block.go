package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// contactJIDParams is the JSON-RPC params shape shared by contact.block and
// contact.unblock: a single JID string.
type contactJIDParams struct {
	JID string `json:"jid"`
}

// blocklistResult is the JSON-RPC result for contact.blocklist. The list is
// the live server-side set; the shape mirrors other list results (a named
// array so future fields like DHash can be added without breaking clients).
type blocklistResult struct {
	Blocked []string `json:"blocked"`
}

// handleContactBlock implements "contact.block" (FR-018).
func (d *Dispatcher) handleContactBlock(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "contact.block", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doContactBlock(ctx, raw)
	})
}

func (d *Dispatcher) doContactBlock(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.blocker == nil {
		return nil, ErrMethodNotFound
	}
	var p contactJIDParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.JID == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.JID)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.blocker.Block(ctx, jid); err != nil {
		return nil, mapBlockErr("contact.block", err)
	}
	return json.Marshal(struct{}{})
}

// handleContactUnblock implements "contact.unblock" (FR-018).
func (d *Dispatcher) handleContactUnblock(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "contact.unblock", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doContactUnblock(ctx, raw)
	})
}

func (d *Dispatcher) doContactUnblock(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.blocker == nil {
		return nil, ErrMethodNotFound
	}
	var p contactJIDParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.JID == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.JID)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.blocker.Unblock(ctx, jid); err != nil {
		return nil, mapBlockErr("contact.unblock", err)
	}
	return json.Marshal(struct{}{})
}

// handleContactBlocklist implements "contact.blocklist" (FR-019). Reads the
// live server-side list — never consults a local cache — so out-of-band
// unblocks (phone UI) are reflected immediately.
func (d *Dispatcher) handleContactBlocklist(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.blocker == nil {
		return nil, ErrMethodNotFound
	}
	list, err := d.blocker.ListBlocked(ctx)
	if err != nil {
		return nil, fmt.Errorf("contact.blocklist: %w", err)
	}
	out := make([]string, 0, len(list))
	for _, j := range list {
		out = append(out, j.String())
	}
	return json.Marshal(blocklistResult{Blocked: out})
}

// mapBlockErr preserves typed domain errors so the socket dispatcher can
// route them to their JSON-RPC codes.
func mapBlockErr(method string, err error) error {
	return fmt.Errorf("%s: %w", method, err)
}

// ensureNotBlocked is the FR-018 pre-send gate. Returns domain.ErrBlocked
// (→ -32100 policy_refused) if the target appears on the live server-side
// blocklist. No-op when Blocker is not wired, preserving the legacy
// send path. Called from handleSend / handleSendMedia / handleReact /
// handleSendReply before the outbound call.
func (d *Dispatcher) ensureNotBlocked(ctx context.Context, jid domain.JID) error {
	if d.blocker == nil {
		return nil
	}
	list, err := d.blocker.ListBlocked(ctx)
	if err != nil {
		// A transient ListBlocked failure must not silently allow the send
		// (CLAUDE.md rule 12: no silent fallbacks). Surface the error so
		// the client can retry.
		return fmt.Errorf("blocker: %w", err)
	}
	for _, b := range list {
		if b == jid {
			return fmt.Errorf("%w: %s", domain.ErrBlocked, jid.String())
		}
	}
	return nil
}
