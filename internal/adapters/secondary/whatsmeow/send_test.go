package whatsmeow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	waClient "go.mau.fi/whatsmeow"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestAdapter(t *testing.T, fc *fakeWhatsmeowClient) *Adapter {
	t.Helper()
	return openWithClient(fc, domain.NewAllowlist(), discardLogger(), fixedNowFn)
}

func TestSend_ValidationFailsBeforeIO(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	// Zero-recipient TextMessage fails Validate().
	_, err := a.Send(context.Background(), domain.TextMessage{Body: "hi"})
	if err == nil {
		t.Fatal("expected validation error; got nil")
	}
	if !errors.Is(err, domain.ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("Send should not reach whatsmeow on validation failure; got %d calls", len(fc.SentMessages))
	}
}

func TestSend_DisconnectedReturnsErrDisconnected(t *testing.T) {
	fc := newFakeClient() // ConnectedFlag=false by default
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"})
	if !errors.Is(err, domain.ErrDisconnected) {
		t.Errorf("want ErrDisconnected; got %v", err)
	}
}

func TestSend_SuccessReturnsMessageIDAndRecordsAudit(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.ABCDEF"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	id, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.ABCDEF" {
		t.Errorf("want wamid.ABCDEF; got %q", id)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage call; got %d", len(fc.SentMessages))
	}
	if got := a.auditBuf.Len(); got != 1 {
		t.Errorf("want 1 audit entry; got %d", got)
	}
}

func TestSend_CtxCancelledBeforeIO(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(ctx, domain.TextMessage{Recipient: to, Body: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("Send must not hit whatsmeow after ctx cancel; got %d calls", len(fc.SentMessages))
	}
}

func TestSend_MediaMessageMissingPath(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Path:      "/nonexistent/path/to/file.jpg",
		Mime:      "image/jpeg",
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist; got %v", err)
	}
}

func TestSend_MediaMessageExistingPathNotImplementedYet(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	// Create a real temp file so os.Stat succeeds.
	tmp := filepath.Join(t.TempDir(), "img.jpg")
	if err := os.WriteFile(tmp, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Path:      tmp,
		Mime:      "image/jpeg",
	})
	if err == nil {
		t.Fatal("want 'not yet implemented' error; got nil")
	}
}
