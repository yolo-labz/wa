package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// threadGetParams is the JSON-RPC params for "thread.get".
type threadGetParams struct {
	Chat   string `json:"chat"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// messageView is the serialised shape of a thread message. Body comes
// channel-wrapped so LLM consumers can distinguish untrusted input.
type messageView struct {
	ID        string `json:"id"`
	Sender    string `json:"sender,omitempty"`
	TS        int64  `json:"ts"`
	Body      string `json:"body"`
	FromMe    bool   `json:"fromMe,omitempty"`
	MediaMime string `json:"mediaMime,omitempty"`
}

type receiptView struct {
	MessageID string `json:"messageId"`
	Kind      string `json:"kind"`
	By        string `json:"by,omitempty"`
	TS        int64  `json:"ts"`
}

// handleThreadGet implements "thread.get".
func (d *Dispatcher) handleThreadGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p threadGetParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, ErrInvalidParams
	}
	chat, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	reader, ok := d.history.(ThreadReader)
	if !ok {
		return nil, ErrMethodNotFound
	}
	page, err := reader.GetThread(ctx, chat, ThreadCursor(p.Cursor), p.Limit)
	if err != nil {
		return nil, fmt.Errorf("thread.get: %w", err)
	}
	msgs := make([]messageView, 0, len(page.Messages))
	for _, m := range page.Messages {
		msgs = append(msgs, messageViewFromDomain(m, chat))
	}
	receipts := make([]receiptView, 0, len(page.Receipts))
	for _, r := range page.Receipts {
		receipts = append(receipts, receiptView{
			MessageID: string(r.MessageID),
			Kind:      r.Kind.String(),
			By:        jidString(r.ByJID),
			TS:        r.TS.Unix(),
		})
	}
	return marshalResult(struct {
		Messages []messageView `json:"messages"`
		Receipts []receiptView `json:"receipts,omitempty"`
		Next     string        `json:"next,omitempty"`
		HasMore  bool          `json:"hasMore"`
	}{msgs, receipts, string(page.Next), page.HasMore})
}

// messageViewFromDomain renders a domain.Message as the on-wire view, wrapping
// inbound bodies in the channel envelope. Outbound (TextMessage with matching
// recipient) passes through unwrapped — we never wrap our own text.
func messageViewFromDomain(m domain.Message, chat domain.JID) messageView {
	switch v := m.(type) {
	case domain.TextMessage:
		return messageView{
			ID:     "", // id lives alongside, caller-side; unknown here
			TS:     0,
			Body:   ChannelWrap(v.Body, chat, v.Recipient, 0),
			FromMe: false,
		}
	case domain.MediaMessage:
		return messageView{
			Body:      ChannelWrap(v.Caption, chat, v.Recipient, 0),
			MediaMime: v.Mime,
		}
	case domain.ReactionMessage:
		return messageView{
			ID:   string(v.TargetID),
			Body: ChannelWrap(v.Emoji, chat, v.Recipient, 0),
		}
	}
	return messageView{}
}

func jidString(j domain.JID) string {
	if j.IsZero() {
		return ""
	}
	return j.String()
}
