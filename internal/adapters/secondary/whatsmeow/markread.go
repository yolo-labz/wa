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

// receiptErr wraps an error with the port method prefix that produced it.
func receiptErr(method string, err error) error {
	return fmt.Errorf("MessageSender.%s: %w", method, err)
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
	return a.markReceipt(ctx, chat, id, false)
}

// MarkPlayed implements app.MessageSender. It sends the same receipt as
// MarkRead with whatsmeow's "played" type appended, which is what makes
// the sender's client render the blue "listened" state on a voice note
// rather than the plain read ticks.
func (a *Adapter) MarkPlayed(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	return a.markReceipt(ctx, chat, id, true)
}

// markReceipt is the shared body of MarkRead and MarkPlayed. played
// selects whatsmeow's ReceiptTypePlayed.
//
// The upstream signature takes the extra type as a vararg, and its own
// doc comment says "providing more than one receipt type will panic: the
// parameter is only a vararg for backwards compatibility". This helper
// therefore builds the slice itself from a bool and never forwards a
// caller-supplied one, so no call path can reach that panic.
func (a *Adapter) markReceipt(ctx context.Context, chat domain.JID, id domain.MessageID, played bool) error {
	method := "MarkRead"
	var extra []waTypes.ReceiptType
	if played {
		method = "MarkPlayed"
		extra = []waTypes.ReceiptType{waTypes.ReceiptTypePlayed}
	}

	if chat.IsZero() {
		return receiptErr(method, domain.ErrInvalidJID)
	}
	if id == "" {
		return receiptErr(method, errors.New("empty message id"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.closed.Load() {
		return receiptErr(method, domain.ErrDisconnected)
	}
	if !a.client.IsConnected() {
		return receiptErr(method, domain.ErrDisconnected)
	}

	waChat := toWhatsmeow(chat)
	ids := []waTypes.MessageID{string(id)}

	sender, err := a.resolveReadReceiptSender(ctx, waChat, string(id))
	if err != nil {
		return receiptErr(method, err)
	}

	if err := a.client.MarkRead(ctx, ids, time.Now(), waChat, sender, extra...); err != nil {
		return receiptErr(method, err)
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
		return waTypes.JID{}, errors.New("group chat sender lookup: history unavailable")
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
