package memory

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

func TestMemoryMediaDoesNotInventChatBinding(t *testing.T) {
	ctx := context.Background()
	store, err := NewMediaStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("synthetic media")
	sum := sha256.Sum256(payload)
	if _, err := store.Write(ctx, domain.MediaRef{SHA256: sum, Mime: "text/plain", Size: int64(len(payload)), Ext: "txt"}, payload, "text/plain", 0); err != nil {
		t.Fatal(err)
	}
	chatA := domain.MustJID("11111111@s.whatsapp.net")
	store.SeedMessageMedia(chatA, "IMAGE", sum)
	if got, err := store.Download(ctx, domain.MustJID("22222222@s.whatsapp.net"), "IMAGE", false); err == nil {
		t.Fatalf("unverified chat gained successful binding: %+v", got)
	}
	for _, chat := range []domain.JID{chatA, {}} {
		got, err := store.Download(ctx, chat, "IMAGE", false)
		if err != nil || got.Chat != chatA || got.Object.Ref.SHA256 != sum {
			t.Fatalf("actual binding: %+v, %v", got, err)
		}
	}
	// Even identical payload hashes do not make a duplicate message ID unique.
	store.SeedMessageMedia(domain.MustJID("22222222@s.whatsapp.net"), "IMAGE", sum)
	if _, err := store.Download(ctx, domain.JID{}, "IMAGE", true); !errors.Is(err, domain.ErrMessageIDAmbiguous) {
		t.Fatalf("ambiguity: %v", err)
	}
}

func TestMemoryQuoteDoesNotIgnoreChat(t *testing.T) {
	a := New(nil)
	chatA := domain.MustJID("11111111@s.whatsapp.net")
	a.SeedQuotedRaw(chatA, "QUOTE", []byte("synthetic quote"))
	if _, err := a.GetRawProto(context.Background(), domain.MustJID("22222222@s.whatsapp.net"), "QUOTE"); err == nil {
		t.Fatal("quote ignored the requested chat")
	}
	if got, err := a.GetRawProto(context.Background(), chatA, "QUOTE"); err != nil || string(got) != "synthetic quote" {
		t.Fatalf("scoped quote: %q, %v", got, err)
	}
}
