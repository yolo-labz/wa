package sqlitehistory

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// This file completes the spec-017 receipt/thread path. The
// `message_receipts` table (schema_v3.sql), the app.ThreadReader port, the
// app.ThreadPage.Receipts field and the `wa thread` JSON output were all
// shipped, but no store ever implemented GetThread/PutReceipt — so the
// table was dead and a caller could never confirm whether a sent message
// was delivered/read (only that the server accepted it). These two methods
// make the Store satisfy app.ThreadReader; the whatsmeow adapter delegates
// to it and persists each inbound ReceiptEvent.

// PutReceipt persists one delivery/read/played receipt. Idempotent on the
// (chat_jid, message_id, jid, kind) primary key — a repeated receipt just
// refreshes ts.
func (s *Store) PutReceipt(ctx context.Context, r domain.MessageReceipt) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("sqlitehistory.PutReceipt: %w", err)
	}
	if r.Chat.IsZero() {
		return fmt.Errorf("sqlitehistory.PutReceipt: %w: chat is zero", domain.ErrReceipt)
	}
	const q = `
INSERT INTO message_receipts (chat_jid, message_id, kind, jid, ts)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (chat_jid, message_id, jid, kind) DO UPDATE SET ts = excluded.ts
`
	if _, err := s.db.ExecContext(
		ctx, q,
		r.Chat.String(), string(r.MessageID), int(r.Kind), r.ByJID.String(), r.TS.Unix(),
	); err != nil {
		return fmt.Errorf("sqlitehistory.PutReceipt: exec: %w", err)
	}
	return nil
}

// GetThread returns one newest-first page of a chat's messages plus every
// receipt recorded for that chat. The message view is the same lossy shape
// LoadMore produces (domain.Message carries no id/ts); the Receipts slice
// is the load-bearing part for delivery confirmation. cursor is the
// message_id to page strictly before (empty = newest); HasMore/Next page
// backwards through history.
func (s *Store) GetThread(ctx context.Context, chat domain.JID, cursor app.ThreadCursor, limit int) (app.ThreadPage, error) {
	if chat.IsZero() {
		return app.ThreadPage{}, fmt.Errorf("sqlitehistory.GetThread: %w", domain.ErrInvalidJID)
	}
	if limit <= 0 {
		limit = 50
	}
	msgs, lastID, err := s.threadMessages(ctx, chat, cursor, limit)
	if err != nil {
		return app.ThreadPage{}, err
	}
	receipts, err := s.receiptsForChat(ctx, chat)
	if err != nil {
		return app.ThreadPage{}, err
	}
	page := app.ThreadPage{Messages: msgs, Receipts: receipts}
	if len(msgs) == limit {
		page.HasMore = true
		page.Next = app.ThreadCursor(lastID)
	}
	return page, nil
}

func (s *Store) threadMessages(ctx context.Context, chat domain.JID, cursor app.ThreadCursor, limit int) ([]domain.Message, string, error) {
	const q = `
SELECT message_id, body, media_type
FROM messages
WHERE chat_jid = ?
  AND (? = '' OR ts < (SELECT ts FROM messages WHERE chat_jid = ? AND message_id = ?))
ORDER BY ts DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, chat.String(), string(cursor), chat.String(), string(cursor), limit)
	if err != nil {
		return nil, "", fmt.Errorf("sqlitehistory.GetThread: messages: %w", err)
	}
	defer closeRows(rows, "GetThread.messages")

	msgs := make([]domain.Message, 0, limit)
	var lastID string
	for rows.Next() {
		var messageID, body, mediaType string
		if err := rows.Scan(&messageID, &body, &mediaType); err != nil {
			return nil, "", fmt.Errorf("sqlitehistory.GetThread: scan: %w", err)
		}
		lastID = messageID
		if mediaType != "" {
			msgs = append(msgs, domain.MediaMessage{Recipient: chat, Mime: mediaType, Caption: body})
		} else {
			msgs = append(msgs, domain.TextMessage{Recipient: chat, Body: body})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("sqlitehistory.GetThread: rows: %w", err)
	}
	return msgs, lastID, nil
}

func (s *Store) receiptsForChat(ctx context.Context, chat domain.JID) ([]domain.MessageReceipt, error) {
	const q = `SELECT message_id, kind, jid, ts FROM message_receipts WHERE chat_jid = ? ORDER BY ts`
	rows, err := s.db.QueryContext(ctx, q, chat.String())
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory.GetThread: receipts: %w", err)
	}
	defer closeRows(rows, "GetThread.receipts")

	var out []domain.MessageReceipt
	for rows.Next() {
		var messageID, jid string
		var kind int
		var ts int64
		if err := rows.Scan(&messageID, &kind, &jid, &ts); err != nil {
			return nil, fmt.Errorf("sqlitehistory.GetThread: receipt scan: %w", err)
		}
		// kind is written by PutReceipt as int(ReceiptStatus) (a uint8); a
		// row outside that range is a corrupt write — skip it rather than
		// wrap-around on the narrowing conversion.
		if kind < 0 || kind > math.MaxUint8 {
			continue
		}
		status := domain.ReceiptStatus(kind)
		if !status.IsValid() {
			continue
		}
		byJID, _ := domain.Parse(jid) // empty jid (self-receipt) → zero JID
		out = append(out, domain.MessageReceipt{
			MessageID: domain.MessageID(messageID),
			Kind:      status,
			TS:        time.Unix(ts, 0),
			ByJID:     byJID,
			Chat:      chat,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory.GetThread: receipt rows: %w", err)
	}
	return out, nil
}
