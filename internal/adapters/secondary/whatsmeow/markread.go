package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// markReadErr wraps an error with the MarkRead method prefix.
func markReadErr(err error) error {
	return fmt.Errorf("MessageSender.MarkRead: %w", err)
}

// MarkRead implements app.MessageSender. It delegates to the whatsmeow
// Client.MarkRead method, translating domain types at the boundary.
//
// whatsmeow receipts require two distinct JIDs in group chats: the chat
// JID (g.us) and the participant JID (s.whatsapp.net) who authored the
// target message. The daemon looks the authoring JID up in messages.db
// via HistoryStore.GetSender. For DMs both slots carry the same value —
// whatsmeow skips the participant attr when chat.Server is a user server
// (DefaultUserServer / HiddenUserServer / MessengerServer), so the slot
// is a no-op there. Feature 018 T1-11 / R-09.
//
// Per research §D3, MarkRead is grouped under MessageSender because it is
// an outbound write operation (sending a read receipt), not a new message
// type.
func (a *Adapter) MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	if chat.IsZero() {
		return markReadErr(domain.ErrInvalidJID)
	}
	if id == "" {
		return markReadErr(fmt.Errorf("empty message id"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.closed.Load() {
		return markReadErr(domain.ErrDisconnected)
	}
	if !a.client.IsConnected() {
		return markReadErr(domain.ErrDisconnected)
	}

	waChat := toWhatsmeow(chat)
	ids := []waTypes.MessageID{string(id)}

	sender, err := a.resolveReadReceiptSender(ctx, waChat, string(id))
	if err != nil {
		return markReadErr(err)
	}

	if err := a.client.MarkRead(ctx, ids, time.Now(), waChat, sender); err != nil {
		return markReadErr(err)
	}
	return nil
}

// resolveReadReceiptSender returns the participant JID for the read
// receipt. DMs (user servers) reuse the chat JID. Groups look up the
// sender in messages.db; a missing or malformed row is a hard error —
// whatsmeow drops the participant attr silently if we pass a zero JID,
// which would regress R-09 to its original behaviour.
func (a *Adapter) resolveReadReceiptSender(ctx context.Context, waChat waTypes.JID, messageID string) (waTypes.JID, error) {
	if isUserServer(waChat.Server) {
		return waChat, nil
	}
	if a.history == nil {
		return waTypes.JID{}, fmt.Errorf("group chat sender lookup: history unavailable")
	}
	senderStr, err := a.history.GetSender(ctx, messageID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return waTypes.JID{}, fmt.Errorf("group chat %s message %s: sender unknown (not in history)", waChat.String(), messageID)
		}
		return waTypes.JID{}, fmt.Errorf("group chat sender lookup: %w", err)
	}
	sender, err := waTypes.ParseJID(senderStr)
	if err != nil {
		return waTypes.JID{}, fmt.Errorf("group chat sender %q: %w", senderStr, err)
	}
	return sender, nil
}

// isUserServer reports whether the whatsmeow server identifier is a user
// (DM-style) server. The three servers listed are exactly the ones
// whatsmeow's receipt encoder treats as no-participant in Client.MarkRead.
func isUserServer(server string) bool {
	switch server {
	case waTypes.DefaultUserServer, waTypes.HiddenUserServer, waTypes.MessengerServer:
		return true
	}
	return false
}
