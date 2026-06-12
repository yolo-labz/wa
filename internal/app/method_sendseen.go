package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// sendSeenParams is the JSON-RPC params for the "sendSeen" method.
type sendSeenParams struct {
	Chat string `json:"chat"`
}

// sendSeenResult reports which message the read receipt was anchored to.
type sendSeenResult struct {
	MessageID string `json:"messageId"`
}

// handleSendSeen implements the "sendSeen" JSON-RPC method: mark the
// chat read at its newest incoming message, without the caller having
// to know a message id (the markRead method needs one; agents driving
// "I have read this conversation" almost never have it at hand). Runs
// the same safety pipeline as markRead (ActionRead) and is wrapped in
// the FR-034a idempotency sidecar.
func (d *Dispatcher) handleSendSeen(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "sendSeen", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendSeen(ctx, raw)
	})
}

func (d *Dispatcher) doSendSeen(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p sendSeenParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}

	finder, ok := d.history.(LatestIncomingFinder)
	if !ok {
		return nil, ErrMethodNotFound
	}

	// Safety pipeline mirrors markRead: read receipts are an outbound,
	// recipient-visible action (FR-009).
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionRead); err != nil {
		return nil, err
	}

	msgID, found, err := finder.LatestIncoming(ctx, jid)
	if err != nil {
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
		return nil, fmt.Errorf("sendSeen: %w", err)
	}
	if !found {
		// Nothing incoming on record — surface the existing -32117
		// message_not_found class rather than silently succeeding (a
		// silent OK would let an agent believe the receipt was sent).
		d.recordAudit(ctx, jid, "error", auditErrDetail(ErrMessageNotFound))
		return nil, fmt.Errorf("sendSeen: no incoming message on record for chat: %w", ErrMessageNotFound)
	}

	if err := d.sender.MarkRead(ctx, jid, msgID); err != nil {
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
		return nil, err
	}

	d.recordAudit(ctx, jid, "ok", string(msgID))

	return marshalResult(sendSeenResult{MessageID: string(msgID)})
}
