package sqlitehistory_test

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// insert is a tiny helper that writes one message row with the fields the
// anchor queries care about (chat, id, ts, direction).
func insert(t *testing.T, s interface {
	InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool, rawProto []byte, senderAltJID, addressingMode string) error
}, chat, id string, ts int64, fromMe bool,
) {
	t.Helper()
	if err := s.InsertRaw(context.Background(), chat, chat, id, ts, "body", "", "", "push", fromMe, nil, "", ""); err != nil {
		t.Fatalf("InsertRaw %s: %v", id, err)
	}
}

// NewestRef / OldestRef pick the right end of the chat by timestamp and
// round-trip the direction flag (stored as INTEGER, read as bool). PR #222.
func TestAnchorRefs_NewestAndOldest(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()
	const chat = "5511111111111@s.whatsapp.net"

	insert(t, s, chat, "m-old", 100, false)
	insert(t, s, chat, "m-mid", 200, true)
	insert(t, s, chat, "m-new", 300, true)

	cj := domain.MustJID(chat)

	newest, ok, err := s.NewestRef(ctx, cj)
	if err != nil || !ok {
		t.Fatalf("NewestRef: ok=%v err=%v", ok, err)
	}
	if string(newest.ID) != "m-new" || newest.Timestamp.Unix() != 300 || !newest.FromMe {
		t.Errorf("newest = %+v, want id=m-new ts=300 fromMe=true", newest)
	}
	if newest.Chat.String() != chat {
		t.Errorf("newest chat = %q, want %q", newest.Chat, chat)
	}

	oldest, ok, err := s.OldestRef(ctx, cj)
	if err != nil || !ok {
		t.Fatalf("OldestRef: ok=%v err=%v", ok, err)
	}
	if string(oldest.ID) != "m-old" || oldest.Timestamp.Unix() != 100 || oldest.FromMe {
		t.Errorf("oldest = %+v, want id=m-old ts=100 fromMe=false", oldest)
	}
}

// An empty chat yields ok=false (no anchor), never an error — the signal
// ForceHistorySync uses to skip a pull instead of nil-dereferencing.
func TestAnchorRefs_EmptyChat(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	_, ok, err := s.NewestRef(context.Background(), domain.MustJID("5511111111111@s.whatsapp.net"))
	if err != nil {
		t.Fatalf("NewestRef on empty chat: %v", err)
	}
	if ok {
		t.Error("want ok=false for a chat with no stored messages")
	}
}

// RecentChats lists chats most-recently-active first — the sweep order for a
// global gap-recovery pull.
func TestRecentChats_OrderedByActivity(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()
	const chatA = "5511111111111@s.whatsapp.net"
	const chatB = "5522222222222@s.whatsapp.net"

	insert(t, s, chatA, "a1", 500, false)
	insert(t, s, chatB, "b1", 900, false) // more recent

	chats, err := s.RecentChats(ctx, 10)
	if err != nil {
		t.Fatalf("RecentChats: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("want 2 chats; got %d (%v)", len(chats), chats)
	}
	if chats[0].String() != chatB {
		t.Errorf("most-recently-active first: got %q, want %q", chats[0], chatB)
	}
}
