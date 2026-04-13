package whatsmeow

import (
	"context"
	"fmt"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/domain"
)

const markReadPrefix = "MessageSender.MarkRead"

// MarkRead implements app.MessageSender. It delegates to the whatsmeow
// Client.MarkRead method, translating domain types at the boundary.
//
// Per research §D3, MarkRead is grouped under MessageSender because it is
// an outbound write operation (sending a read receipt), not a new message
// type.
func (a *Adapter) MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	if chat.IsZero() {
		return fmt.Errorf("%s: %w", markReadPrefix, domain.ErrInvalidJID)
	}
	if id == "" {
		return fmt.Errorf("%s: empty message id", markReadPrefix)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.closed.Load() {
		return fmt.Errorf("%s: %w", markReadPrefix, domain.ErrDisconnected)
	}
	if !a.client.IsConnected() {
		return fmt.Errorf("%s: %w", markReadPrefix, domain.ErrDisconnected)
	}

	waChat := toWhatsmeow(chat)
	ids := []waTypes.MessageID{string(id)}

	if err := a.client.MarkRead(ctx, ids, time.Now(), waChat, waChat); err != nil {
		return fmt.Errorf("%s: %w", markReadPrefix, err)
	}
	return nil
}
