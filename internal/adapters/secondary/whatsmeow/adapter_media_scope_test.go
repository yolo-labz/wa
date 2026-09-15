package whatsmeow

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestDownloadScopesDuplicateIDsBeforeColdOrWarmCache(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-a", true: "first-b"}[reverse], func(t *testing.T) {
			testMediaScope(t, reverse)
		})
	}
}

func testMediaScope(t *testing.T, reverse bool) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	chatA, chatB := "11111111@s.whatsapp.net", "22222222@s.whatsapp.net"
	payloads := [][]byte{[]byte("payload-a"), []byte("payload-b")}
	hashes := [][32]byte{sha256.Sum256(payloads[0]), sha256.Sum256(payloads[1])}
	chats := []string{chatA, chatB}
	if reverse {
		chats[0], chats[1] = chats[1], chats[0]
	}
	for i, chat := range chats {
		payloadIndex := 0
		if chat == chatB {
			payloadIndex = 1
		}
		msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			FileSHA256: hashes[payloadIndex][:], FileLength: proto.Uint64(uint64(len(payloads[payloadIndex]))), Mimetype: proto.String("image/jpeg"),
		}}
		raw, marshalErr := proto.Marshal(msg)
		if marshalErr != nil {
			t.Fatalf("marshal: %v", marshalErr)
		}
		if err := store.InsertRaw(ctx, chat, chat, "DUP", int64(i+1), "", "image/jpeg", "", "", false, raw, "", ""); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	fc := newFakeClient()
	adapter := newTestAdapter(t, fc)
	adapter.history = store
	media, err := adapter.NewMediaAdapter(t.TempDir())
	if err != nil {
		t.Fatalf("media adapter: %v", err)
	}
	var downloads atomic.Int32
	fc.DownloadAnyFn = func(_ context.Context, msg *waE2E.Message) ([]byte, error) {
		downloads.Add(1)
		got := msg.GetImageMessage().GetFileSHA256()
		for i := range hashes {
			if string(got) == string(hashes[i][:]) {
				return append([]byte(nil), payloads[i]...), nil
			}
		}
		return nil, errors.New("unexpected hash")
	}
	if _, err := media.Download(ctx, domain.JID{}, "DUP", false); !errors.Is(err, domain.ErrMessageIDAmbiguous) {
		t.Fatalf("cold unqualified duplicate = %v", err)
	}
	if downloads.Load() != 0 {
		t.Fatal("cold ambiguous lookup reached downloader")
	}
	if _, err := media.Download(ctx, domain.MustJID("33333333@s.whatsapp.net"), "DUP", false); !errors.Is(err, app.ErrMessageNotFound) {
		t.Fatalf("qualified cross-chat miss = %v", err)
	}
	if downloads.Load() != 0 {
		t.Fatal("qualified miss reached downloader")
	}

	for i, chat := range []string{chatA, chatB} {
		jid := domain.MustJID(chat)
		cold, err := media.Download(ctx, jid, "DUP", false)
		if err != nil || cold.Cached || cold.Object.Ref.SHA256 != hashes[i] || cold.Chat != jid {
			t.Fatalf("cold %s = %+v, %v", chat, cold, err)
		}
		warm, err := media.Download(ctx, jid, "DUP", false)
		if err != nil || !warm.Cached || warm.Object.Ref.SHA256 != hashes[i] || warm.Chat != jid {
			t.Fatalf("warm %s = %+v, %v", chat, warm, err)
		}
		info, present, err := media.InspectMedia(ctx, chat, "DUP")
		if err != nil || !present || info.SHA256 != cold.Object.Ref.HexSHA256() {
			t.Fatalf("inspect %s = %+v, %v", chat, info, err)
		}
	}
	before := downloads.Load()
	if _, err := media.Download(ctx, domain.JID{}, "DUP", false); !errors.Is(err, domain.ErrMessageIDAmbiguous) {
		t.Fatalf("unqualified duplicate = %v", err)
	}
	if downloads.Load() != before {
		t.Fatal("ambiguous lookup reached downloader")
	}
	// A legacy or corrupt row must not fall back to the other chat's warm CAS.
	_, validRaw, err := store.GetRawProto(ctx, chatB, "DUP")
	if err != nil {
		t.Fatal(err)
	}
	// A corrupt stored identity cannot truthfully bind even a warm CAS object.
	if err := store.InsertRaw(ctx, "not-a-jid", "not-a-jid", "BAD-CHAT", 5, "", "image/jpeg", "", "", false, validRaw, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := media.Download(ctx, domain.JID{}, "BAD-CHAT", false); !errors.Is(err, domain.ErrInvalidPhone) {
		t.Fatalf("invalid stored chat was not refused: %v", err)
	}
	if downloads.Load() != before {
		t.Fatal("invalid stored chat reached downloader")
	}
	for mid, raw := range map[string][]byte{"LEGACY": nil, "CORRUPT": {0xff}} {
		for chat, data := range map[string][]byte{chatA: raw, chatB: validRaw} {
			if err := store.InsertRaw(ctx, chat, chat, mid, 5, "", "image/jpeg", "", "", false, data, "", ""); err != nil {
				t.Fatalf("insert bad/valid pair: %v", err)
			}
		}
		if _, err := media.Download(ctx, domain.MustJID(chatA), domain.MessageID(mid), false); err == nil {
			t.Fatal("legacy/corrupt selected proto fell through to another chat")
		}
		if _, err := media.Download(ctx, domain.JID{}, domain.MessageID(mid), false); !errors.Is(err, domain.ErrMessageIDAmbiguous) {
			t.Fatalf("proto validity changed ambiguity: %v", err)
		}
		if downloads.Load() != before {
			t.Fatal("legacy/corrupt selected proto reached downloader")
		}
	}
}
