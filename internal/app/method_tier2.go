package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// sendReplyParams is the JSON-RPC params for the "send.reply" method
// (FR-070). QuotedID is the message being quoted; Body is the reply text.
type sendReplyParams struct {
	To       string `json:"to"`
	QuotedID string `json:"quotedId"`
	Body     string `json:"body"`
}

// composingParams is the JSON-RPC params for "chat.composing" (FR-071).
// State is "composing", "recording", or "paused".
type composingParams struct {
	Chat     string `json:"chat"`
	State    string `json:"state"`
	Duration int64  `json:"durationMs,omitempty"`
}

// groupsGetParams is the JSON-RPC params for "groups.get" (FR-072).
type groupsGetParams struct {
	JID string `json:"jid"`
}

// groupsGetResult is the JSON-RPC result for "groups.get".
type groupsGetResult struct {
	JID          string   `json:"jid"`
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
	Admins       []string `json:"admins"`
	FetchedAt    int64    `json:"fetchedAt"`
}

// handleSendReply implements "send.reply" (FR-070). Requires a ReplySender
// adapter; returns ErrMethodNotFound when the port is not wired. Wrapped
// in the FR-034a idempotency sidecar.
func (d *Dispatcher) handleSendReply(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "send.reply", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendReply(ctx, raw)
	})
}

func (d *Dispatcher) doSendReply(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	rs, ok := d.sender.(ReplySender)
	if !ok {
		return nil, ErrMethodNotFound
	}
	var p sendReplyParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.QuotedID == "" || p.Body == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}
	msg := domain.TextMessage{Recipient: jid, Body: p.Body}
	id, err := rs.SendReply(ctx, domain.MessageID(p.QuotedID), msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
		return nil, fmt.Errorf("send.reply: %w", err)
	}
	d.recordAudit(ctx, jid, "ok", string(id))
	return marshalResult(sendResult{
		MessageID: string(id),
		Timestamp: time.Now().Unix(),
	})
}

// handleComposing implements "chat.composing" (FR-071).
func (d *Dispatcher) handleComposing(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	ps, ok := d.presence()
	if !ok {
		return nil, ErrMethodNotFound
	}
	var p composingParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" || p.State == "" {
		return nil, ErrInvalidParams
	}
	switch p.State {
	case "composing", "recording", "paused":
	default:
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionPresence); err != nil {
		return nil, err
	}
	if err := ps.SendComposing(ctx, jid, p.State, time.Duration(p.Duration)*time.Millisecond); err != nil {
		return nil, fmt.Errorf("chat.composing: %w", err)
	}
	return json.Marshal(struct{}{})
}

// handleGroupsGet implements "groups.get" (FR-072).
func (d *Dispatcher) handleGroupsGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p groupsGetParams
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
	if !jid.IsGroup() {
		return nil, ErrInvalidJID
	}
	g, err := d.groups.Get(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf("groups.get: %w", err)
	}
	parts := make([]string, len(g.Participants))
	for i, p := range g.Participants {
		parts[i] = p.String()
	}
	admins := make([]string, len(g.Admins))
	for i, a := range g.Admins {
		admins[i] = a.String()
	}
	return marshalResult(groupsGetResult{
		JID:          g.JID.String(),
		Subject:      g.Subject,
		Participants: parts,
		Admins:       admins,
		FetchedAt:    g.FetchedAt.Unix(),
	})
}

func (d *Dispatcher) presence() (PresenceSender, bool) {
	ps, ok := d.presenceSender.(PresenceSender)
	return ps, ok && ps != nil
}
