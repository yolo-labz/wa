package sqlitehistory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestGetMessageIdentity covers the columns feature 115's deleteMessageForMe
// index is built from (FR-115-2, FR-115-3). It runs against a real migrated
// Store rather than a fake because the thing that can break is the SQL: a
// renamed column, or `is_from_me` (an INTEGER) failing to scan into a Go
// bool. Neither is observable from the whatsmeow-side fake.
func TestGetMessageIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	chat, err := domain.Parse("120363000000000001@g.us")
	if err != nil {
		t.Fatalf("Parse chat: %v", err)
	}

	const (
		mine   = "3EB0MINE115"
		theirs = "3EB0THEIRS115"
	)
	if err := store.Insert(ctx, []StoredMessage{
		{
			ChatJID:   chat.String(),
			SenderJID: "5511999990001@s.whatsapp.net",
			MessageID: mine,
			Timestamp: 1788600000,
			Body:      "sent by me",
			IsFromMe:  true,
		},
		{
			ChatJID:   chat.String(),
			SenderJID: "5511922222222@s.whatsapp.net",
			MessageID: theirs,
			Timestamp: 1788600060,
			Body:      "sent by someone else",
			IsFromMe:  false,
		},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	t.Run("from_me", func(t *testing.T) {
		sender, fromMe, ts, err := store.GetMessageIdentity(ctx, chat, mine)
		if err != nil {
			t.Fatalf("GetMessageIdentity: %v", err)
		}
		if !fromMe {
			t.Error("fromMe: want true")
		}
		if sender != "5511999990001@s.whatsapp.net" {
			t.Errorf("sender: got %q", sender)
		}
		if ts != 1788600000 {
			t.Errorf("ts: want 1788600000, got %d", ts)
		}
	})

	t.Run("from_third_party", func(t *testing.T) {
		sender, fromMe, ts, err := store.GetMessageIdentity(ctx, chat, theirs)
		if err != nil {
			t.Fatalf("GetMessageIdentity: %v", err)
		}
		if fromMe {
			t.Error("fromMe: want false")
		}
		if sender != "5511922222222@s.whatsapp.net" {
			t.Errorf("sender: got %q", sender)
		}
		if ts != 1788600060 {
			t.Errorf("ts: want 1788600060, got %d", ts)
		}
	})

	// FR-115-7: the caller must be able to tell "no such message" apart from
	// a database fault, because only the first one is the caller's to fix.
	t.Run("missing_row_is_not_exist", func(t *testing.T) {
		_, _, _, err := store.GetMessageIdentity(ctx, chat, "3EB0NOSUCHMESSAGE")
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want wrapped os.ErrNotExist, got %v", err)
		}
	})

	t.Run("rejects_empty_id", func(t *testing.T) {
		if _, _, _, err := store.GetMessageIdentity(ctx, chat, ""); err == nil {
			t.Fatal("want an error for an empty message id")
		}
	})

	t.Run("rejects_zero_chat", func(t *testing.T) {
		if _, _, _, err := store.GetMessageIdentity(ctx, domain.JID{}, mine); !errors.Is(err, domain.ErrInvalidJID) {
			t.Fatalf("want ErrInvalidJID, got %v", err)
		}
	})
}
