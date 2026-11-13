package whatsmeow

import (
	"context"
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

// TestDownloadRefusesRevokedMessage (p8 REVOKED-TEXT M1): a revoked voice
// note, either scope, neither downloads nor transcribes by message id, and
// InspectMedia reports no media for it. A live twin still downloads.
func TestDownloadRefusesRevokedMessage(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	chat := domain.MustJID("11111111@s.whatsapp.net")
	raw, err := proto.Marshal(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		FileSHA256: make([]byte, 32), FileLength: proto.Uint64(4), Mimetype: proto.String("audio/ogg"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"live", "everyone", "self"} {
		if err := store.InsertRaw(ctx, domain.HistoryRecord{
			ChatJID: chat.String(), SenderJID: chat.String(), MessageID: id, Timestamp: int64(i + 1), MediaType: "audio/ogg", RawProto: raw,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for id, scope := range map[domain.MessageID]domain.RevokeScope{"everyone": domain.RevokeEveryone, "self": domain.RevokeSelf} {
		if err := store.StampRevoke(ctx, chat, id, scope, 1_700_000_000); err != nil {
			t.Fatal(err)
		}
	}
	fc := newFakeClient()
	adapter := newTestAdapter(t, fc)
	adapter.history = store
	media, err := adapter.NewMediaAdapter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	fc.DownloadAnyFn = func(context.Context, *waE2E.Message) ([]byte, error) {
		downloads.Add(1)
		return []byte("opus"), nil
	}
	for _, id := range []domain.MessageID{"everyone", "self"} {
		for _, transcribe := range []bool{false, true} {
			if _, err := media.Download(ctx, chat, id, transcribe); !errors.Is(err, app.ErrMessageNotFound) {
				t.Errorf("Download(%s, transcribe=%v) = %v, want ErrMessageNotFound", id, transcribe, err)
			}
		}
		if _, present, err := media.InspectMedia(ctx, chat.String(), string(id)); err != nil || present {
			t.Errorf("InspectMedia(%s) = present %v, %v; want absent", id, present, err)
		}
	}
	if downloads.Load() != 0 {
		t.Fatalf("a revoked message reached the downloader %d times", downloads.Load())
	}
	if _, err := media.Download(ctx, chat, "live", false); err != nil {
		t.Fatalf("live twin: %v", err)
	}
}
