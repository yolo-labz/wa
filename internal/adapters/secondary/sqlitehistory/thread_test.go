package sqlitehistory_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestPutReceiptGetThreadRoundtrip proves the receipt path that was
// plumbed but never implemented: a delivery/read receipt persisted via
// PutReceipt is returned by GetThread, idempotently per kind. This is what
// makes "did my send actually land?" answerable post-hoc.
func TestPutReceiptGetThreadRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	chat := domain.MustJID("5581999999999@s.whatsapp.net")
	if err := s.InsertRaw(ctx, chat.String(), "5581988888888@s.whatsapp.net", "MSG-1",
		1_700_000_000, "oi", "", "", "Me", true, nil, "", ""); err != nil {
		t.Fatalf("InsertRaw: %v", err)
	}

	// Before any receipt: message present, zero receipts.
	page, err := s.GetThread(ctx, chat, "", 10)
	if err != nil {
		t.Fatalf("GetThread pre: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(page.Messages))
	}
	if len(page.Receipts) != 0 {
		t.Fatalf("receipts = %d, want 0 before any PutReceipt", len(page.Receipts))
	}

	// Persist delivered + read for the same message.
	for _, k := range []domain.ReceiptStatus{domain.ReceiptDelivered, domain.ReceiptRead} {
		if err := s.PutReceipt(ctx, domain.MessageReceipt{
			MessageID: "MSG-1", Kind: k, TS: time.Unix(1_700_000_100, 0), ByJID: chat, Chat: chat,
		}); err != nil {
			t.Fatalf("PutReceipt %v: %v", k, err)
		}
	}
	// Idempotent on (chat,msg,jid,kind): re-putting delivered does not duplicate.
	if err := s.PutReceipt(ctx, domain.MessageReceipt{
		MessageID: "MSG-1", Kind: domain.ReceiptDelivered, TS: time.Unix(1_700_000_200, 0), ByJID: chat, Chat: chat,
	}); err != nil {
		t.Fatalf("PutReceipt re-delivered: %v", err)
	}

	page, err = s.GetThread(ctx, chat, "", 10)
	if err != nil {
		t.Fatalf("GetThread post: %v", err)
	}
	if len(page.Receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (delivered+read, idempotent)", len(page.Receipts))
	}
	kinds := map[domain.ReceiptStatus]bool{}
	for _, r := range page.Receipts {
		if r.MessageID != "MSG-1" {
			t.Errorf("receipt MessageID = %q, want MSG-1", r.MessageID)
		}
		kinds[r.Kind] = true
	}
	if !kinds[domain.ReceiptDelivered] || !kinds[domain.ReceiptRead] {
		t.Errorf("receipt kinds = %v, want {delivered, read}", kinds)
	}

	// A definitively non-WhatsApp PutReceipt with a zero chat is rejected.
	if err := s.PutReceipt(ctx, domain.MessageReceipt{
		MessageID: "MSG-1", Kind: domain.ReceiptRead, TS: time.Unix(1_700_000_300, 0),
	}); err == nil {
		t.Error("PutReceipt with zero Chat: want error, got nil")
	}
}
