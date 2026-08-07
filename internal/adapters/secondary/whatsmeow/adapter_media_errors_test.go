package whatsmeow

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// mediaHistory is a configurable historyContainer stub for media.download
// error-path tests. Only GetRawProto is exercised; the rest are no-ops.
type mediaHistory struct {
	chatJID  string
	rawProto []byte
	err      error
}

func (s *mediaHistory) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	return nil, nil
}

func (s *mediaHistory) InsertDomainMessages(ctx context.Context, msgs []domain.Message) error {
	return nil
}

func (s *mediaHistory) InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool, rawProto []byte, senderAltJID, addressingMode string) error {
	return nil
}

func (s *mediaHistory) InsertRawInteractive(_ context.Context, _, _, _ string, _ int64, _, _, _, _ string, _ bool, _ []byte, _, _ string, _ []byte) error {
	return nil
}

func (s *mediaHistory) GetRawProto(ctx context.Context, messageID string) (string, []byte, error) {
	return s.chatJID, s.rawProto, s.err
}

func (s *mediaHistory) GetSender(ctx context.Context, messageID string) (string, error) {
	return "", os.ErrNotExist
}

func (s *mediaHistory) Search(ctx context.Context, query string, limit int) ([]domain.Message, error) {
	return nil, nil
}
func (s *mediaHistory) Close() error { return nil }

func (s *mediaHistory) NewestRef(context.Context, domain.JID) (domain.MessageRef, bool, error) {
	return domain.MessageRef{}, false, nil
}

func (s *mediaHistory) OldestRef(context.Context, domain.JID) (domain.MessageRef, bool, error) {
	return domain.MessageRef{}, false, nil
}

func (s *mediaHistory) RecentChats(context.Context, int) ([]domain.JID, error) {
	return nil, nil
}

func (s *mediaHistory) LatestIncoming(context.Context, domain.JID) (domain.MessageID, bool, error) {
	return "", false, nil
}

func (s *mediaHistory) PutReceipt(_ context.Context, _ domain.MessageReceipt) error { return nil }

func (s *mediaHistory) GetThread(_ context.Context, _ domain.JID, _ app.ThreadCursor, _ int) (app.ThreadPage, error) {
	return app.ThreadPage{}, nil
}

func newMediaAdapterForTest(t *testing.T, hist historyContainer) *MediaAdapter {
	t.Helper()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.history = hist
	m, err := a.NewMediaAdapter(t.TempDir())
	if err != nil {
		t.Fatalf("NewMediaAdapter: %v", err)
	}
	// Guard: DownloadAny must never fire for the failure modes under test.
	fc.DownloadAnyFn = func(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
		t.Fatal("DownloadAny called: type-check should short-circuit before network")
		return nil, nil
	}
	return m
}

// TestDownload_NonMediaMessageReturnsErrMediaUnsupported pins issue #102:
// a message whose proto has no image/video/audio/document/sticker
// sub-message MUST fail with domain.ErrMediaUnsupported (permanent;
// caller MUST NOT retry), never with a generic error or os.ErrNotExist.
func TestDownload_NonMediaMessageReturnsErrMediaUnsupported(t *testing.T) {
	textOnly := &waE2E.Message{Conversation: new("hi there")}
	blob, err := proto.Marshal(textOnly)
	if err != nil {
		t.Fatalf("marshal text proto: %v", err)
	}
	hist := &mediaHistory{chatJID: "120363@g.us", rawProto: blob}
	m := newMediaAdapterForTest(t, hist)

	_, err = m.Download(context.Background(), domain.MessageID("3ADC343BAD95E8A638CE"), false)
	if err == nil {
		t.Fatal("Download on text-only proto: want error; got nil")
	}
	if !errors.Is(err, domain.ErrMediaUnsupported) {
		t.Errorf("want ErrMediaUnsupported; got %v", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Error("regression: text-only message should NOT return os.ErrNotExist (issue #102)")
	}
}

// TestDownload_EmptyRawProtoReturnsErrMediaNotCached pins issue #102:
// a row whose raw_proto column is empty (legacy pre-v3 schema) MUST
// fail with domain.ErrMediaNotCached so the caller can prompt for
// `wa migrate` instead of seeing -32603 Internal error. The legacy
// behaviour of wrapping os.ErrNotExist is intentionally retired —
// callers MUST switch to the typed sentinel.
func TestDownload_EmptyRawProtoReturnsErrMediaNotCached(t *testing.T) {
	hist := &mediaHistory{chatJID: "120363@g.us", rawProto: nil}
	m := newMediaAdapterForTest(t, hist)

	_, err := m.Download(context.Background(), domain.MessageID("LEGACY-ID"), false)
	if err == nil {
		t.Fatal("Download on empty raw_proto: want error; got nil")
	}
	if !errors.Is(err, domain.ErrMediaNotCached) {
		t.Errorf("want ErrMediaNotCached; got %v", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Error("regression: empty raw_proto must NOT wrap os.ErrNotExist (issue #102 typed-error rewrite)")
	}
}

// TestDownload_HistoryMissReturnsErrMessageNotFound pins issue #312: an
// id with NO row at all is bad input, not a cache miss, so it MUST surface
// as app.ErrMessageNotFound (-32117, exit 64, "no such message id") rather
// than ErrMediaNotCached.
//
// It answered ErrMediaNotCached until #312. That code is documented
// "recoverable via re-sync", which reads as "WhatsApp expired the media" —
// a real triage session spent two sittings asking a person to resend a file
// that had never existed. The distinction between "you typed an id I don't
// have" and "I have the row but not the bytes" is the whole point.
func TestDownload_HistoryMissReturnsErrMessageNotFound(t *testing.T) {
	hist := &mediaHistory{err: os.ErrNotExist}
	m := newMediaAdapterForTest(t, hist)

	_, err := m.Download(context.Background(), domain.MessageID("UNKNOWN"), false)
	if err == nil {
		t.Fatal("Download on history miss: want error; got nil")
	}
	if !errors.Is(err, app.ErrMessageNotFound) {
		t.Errorf("want ErrMessageNotFound; got %v", err)
	}
	if errors.Is(err, domain.ErrMediaNotCached) {
		t.Error("regression: an unknown id must NOT report as a recoverable cache miss (issue #312)")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Error("regression: history miss must NOT wrap os.ErrNotExist (issue #102 typed-error rewrite)")
	}
	if !strings.Contains(err.Error(), "UNKNOWN") {
		t.Errorf("error must name the rejected id; got %q", err.Error())
	}
}
