package whatsmeow

import (
	"context"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// PutReceipt and GetThread make *Adapter satisfy app.ThreadReader by
// delegating to the underlying history store. The dispatcher reaches them
// via a type-assert on its HistoryStore port (method_thread.go), so
// wiring the adapter as History also lights up `wa thread`'s receipts.
func (a *Adapter) PutReceipt(ctx context.Context, r domain.MessageReceipt) error {
	if a.history == nil {
		return fmt.Errorf("whatsmeow: history store not configured")
	}
	return a.history.PutReceipt(ctx, r)
}

// GetThread returns one page of a chat's messages plus its recorded
// receipts, delegating to the underlying history store.
func (a *Adapter) GetThread(ctx context.Context, chat domain.JID, cursor app.ThreadCursor, limit int) (app.ThreadPage, error) {
	if a.history == nil {
		return app.ThreadPage{}, fmt.Errorf("whatsmeow: history store not configured")
	}
	return a.history.GetThread(ctx, chat, cursor, limit)
}

// persistReceipt maps an inbound ReceiptEvent to a MessageReceipt and
// stores it. Best-effort + nil-safe: a missing store or a write error is
// logged, never propagated, so receipt persistence cannot break the event
// pipeline. ByJID is set to the chat (exact for 1:1; an approximation for
// groups, where the per-reader JID is not carried on ReceiptEvent).
func (a *Adapter) persistReceipt(re domain.ReceiptEvent) {
	if a.history == nil {
		return
	}
	r := domain.MessageReceipt{
		MessageID: re.MessageID,
		Kind:      re.Status,
		TS:        re.TS,
		ByJID:     re.Chat,
		Chat:      re.Chat,
	}
	if err := a.history.PutReceipt(a.clientCtx, r); err != nil {
		a.logger.Warn("persist receipt failed", "error", err, "message_id", string(re.MessageID))
	}
}
