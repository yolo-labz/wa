package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ThreadReaderFactory returns a fresh ThreadReader for one sub-test.
type ThreadReaderFactory func(t *testing.T) app.ThreadReader

// ThreadSeeder is the optional hook TR4 needs: store a message in chat,
// revoked through the reader's own revoke path in scope (the zero scope
// leaves it live).
type ThreadSeeder interface {
	SeedThread(chat domain.JID, id domain.MessageID, msg domain.Message, scope domain.RevokeScope)
}

// RunThreadReaderContract exercises ThreadReader (FR-010..013).
//
//	TR1 GetThread on an empty chat returns empty Messages + Next="" + HasMore=false.
//	TR2 PutReceipt persists a valid MessageReceipt (nil error).
//	TR3 PutReceipt rejects a zero MessageID with a non-nil error.
//	TR4 GetThread returns a revoked message as revoked, without its text.
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

	t.Run("TR4_revoked_text_hidden", func(t *testing.T) {
		r := factory(t)
		seed, ok := r.(ThreadSeeder)
		if !ok {
			t.Skip("reader cannot seed messages")
		}
		seed.SeedThread(chat, "live", domain.TextMessage{Recipient: chat, Body: "still here"}, 0)
		seed.SeedThread(chat, "gone", domain.TextMessage{Recipient: chat, Body: revokedText}, domain.RevokeEveryone)
		seed.SeedThread(chat, "gone-media", domain.MediaMessage{Recipient: chat, Mime: "image/jpeg", Caption: revokedText}, domain.RevokeEveryone)
		seed.SeedThread(chat, "gone-self", domain.TextMessage{Recipient: chat, Body: revokedText}, domain.RevokeSelf)
		page, err := r.GetThread(ctx, chat, "", 50)
		if err != nil {
			reportf(t, "ThreadReader", "GetThread", "TR4", "nil error", err.Error())
			return
		}
		seen := map[string]app.ThreadMessage{}
		for _, m := range page.Messages {
			seen[m.ID] = m
		}
		for _, id := range []string{"gone", "gone-media", "gone-self"} {
			if m, ok := seen[id]; !ok || !m.Revoked || m.Message.Content().Text != "" {
				reportf(t, "ThreadReader", "GetThread", "TR4", id+" revoked with no text", "text="+m.Message.Content().Text)
			}
		}
		if m := seen["live"]; m.Revoked || m.Message.Content().Text != "still here" {
			reportf(t, "ThreadReader", "GetThread", "TR4", "live message intact", "text="+m.Message.Content().Text)
		}
	})

	t.Run("TR2_put_receipt_valid", func(t *testing.T) {
		r := factory(t)
		rec := domain.MessageReceipt{
			MessageID: domain.MessageID("wamid.ABC"),
			Kind:      domain.ReceiptDelivered,
			TS:        time.Unix(1_700_000_000, 0).UTC(),
			Chat:      chat,
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
