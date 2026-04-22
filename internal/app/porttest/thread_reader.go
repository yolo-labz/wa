package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// ThreadReaderFactory returns a fresh ThreadReader for one sub-test.
type ThreadReaderFactory func(t *testing.T) app.ThreadReader

// RunThreadReaderContract exercises ThreadReader (FR-010..013).
//
//	TR1 GetThread on an empty chat returns empty Messages + Next="" + HasMore=false.
//	TR2 PutReceipt persists a valid MessageReceipt (nil error).
//	TR3 PutReceipt rejects a zero MessageID with a non-nil error.
func RunThreadReaderContract(t *testing.T, factory ThreadReaderFactory) {
	t.Helper()
	ctx := context.Background()
	chat := domain.MustJID("5511999999999")

	t.Run("TR1_empty_thread", func(t *testing.T) {
		r := factory(t)
		page, err := r.GetThread(ctx, chat, "", 50)
		if err != nil {
			reportf(t, "ThreadReader", "GetThread", "TR1", "nil error", err.Error())
			return
		}
		if len(page.Messages) != 0 {
			reportf(t, "ThreadReader", "GetThread", "TR1", "0 messages", itoa(len(page.Messages)))
		}
		if page.HasMore {
			reportf(t, "ThreadReader", "GetThread", "TR1", "HasMore=false", "HasMore=true")
		}
	})

	t.Run("TR2_put_receipt_valid", func(t *testing.T) {
		r := factory(t)
		rec := domain.MessageReceipt{
			MessageID: domain.MessageID("wamid.ABC"),
			Kind:      domain.ReceiptDelivered,
			TS:        time.Unix(1_700_000_000, 0).UTC(),
		}
		if err := r.PutReceipt(ctx, rec); err != nil {
			reportf(t, "ThreadReader", "PutReceipt", "TR2", "nil error", err.Error())
		}
	})

	t.Run("TR3_put_receipt_zero_id_rejected", func(t *testing.T) {
		r := factory(t)
		rec := domain.MessageReceipt{
			Kind: domain.ReceiptDelivered,
			TS:   time.Unix(1_700_000_000, 0).UTC(),
		}
		if err := r.PutReceipt(ctx, rec); err == nil {
			reportf(t, "ThreadReader", "PutReceipt", "TR3", "non-nil error", "nil")
		}
	})
}
