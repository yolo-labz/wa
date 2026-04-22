package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/app/porttest"
	"github.com/yolo-labz/wa/internal/domain"
)

// TestChatStateAdapter_SatisfiesContract drives the ChatStateManager contract
// (CS1..CS7) against the whatsmeow adapter with a fake client.
func TestChatStateAdapter_SatisfiesContract(t *testing.T) {
	porttest.RunChatStateManagerContract(t, func(t *testing.T) app.ChatStateManager {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
		t.Cleanup(func() { _ = a.Close() })
		cs, err := a.NewChatStateFor()
		if err != nil {
			t.Fatalf("NewChatStateFor: %v", err)
		}
		return cs
	})
}

func newTestChatState(t *testing.T, nowFn func() time.Time) (*ChatStateAdapter, *fakeWhatsmeowClient) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), nowFn)
	cs, err := a.NewChatStateFor()
	if err != nil {
		t.Fatalf("NewChatStateFor: %v", err)
	}
	return cs, fc
}

// TestArchiveToggle verifies two Archive(true) calls succeed and both push
// an appstate patch to the wire (idempotency is server-side, not local).
func TestArchiveToggle(t *testing.T) {
	cs, fc := newTestChatState(t, time.Now)
	chat := domain.MustJID("5511999999999")
	ctx := context.Background()

	if err := cs.Archive(ctx, chat, true); err != nil {
		t.Fatalf("Archive(true) #1: %v", err)
	}
	if err := cs.Archive(ctx, chat, true); err != nil {
		t.Fatalf("Archive(true) #2: %v", err)
	}
	if err := cs.Archive(ctx, chat, false); err != nil {
		t.Fatalf("Archive(false): %v", err)
	}
	if n := len(fc.AppStatePatches); n != 3 {
		t.Fatalf("want 3 appstate patches, got %d", n)
	}
}

// TestMuteFutureOnly verifies nil unmutes, future ts mutes, past ts is
// refused with domain.ErrPastMuteTimestamp.
func TestMuteFutureOnly(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0).UTC()
	cs, fc := newTestChatState(t, func() time.Time { return fixed })
	chat := domain.MustJID("5511999999999")
	ctx := context.Background()

	if err := cs.Mute(ctx, chat, nil); err != nil {
		t.Fatalf("Mute(nil) unmute: %v", err)
	}
	future := fixed.Add(time.Hour)
	if err := cs.Mute(ctx, chat, &future); err != nil {
		t.Fatalf("Mute(future): %v", err)
	}
	past := fixed.Add(-time.Hour)
	err := cs.Mute(ctx, chat, &past)
	if !errors.Is(err, domain.ErrPastMuteTimestamp) {
		t.Fatalf("Mute(past) err = %v, want ErrPastMuteTimestamp", err)
	}
	if n := len(fc.AppStatePatches); n != 2 {
		t.Fatalf("want 2 appstate patches (nil + future), got %d", n)
	}
}

// TestPinCap3UpstreamError verifies the 4th distinct pin attempt is
// refused with ErrUpstreamError BEFORE the appstate patch is sent.
// Unpinning frees a slot so a subsequent pin succeeds.
func TestPinCap3UpstreamError(t *testing.T) {
	cs, fc := newTestChatState(t, time.Now)
	ctx := context.Background()
	chats := []domain.JID{
		domain.MustJID("5511111111111"),
		domain.MustJID("5511222222222"),
		domain.MustJID("5511333333333"),
	}
	for i, c := range chats {
		if err := cs.Pin(ctx, c, true); err != nil {
			t.Fatalf("Pin #%d: %v", i+1, err)
		}
	}
	if n := len(fc.AppStatePatches); n != 3 {
		t.Fatalf("after 3 pins want 3 patches, got %d", n)
	}

	fourth := domain.MustJID("5511444444444")
	err := cs.Pin(ctx, fourth, true)
	if !errors.Is(err, domain.ErrUpstreamError) {
		t.Fatalf("4th pin err = %v, want ErrUpstreamError", err)
	}
	if n := len(fc.AppStatePatches); n != 3 {
		t.Fatalf("refused pin must NOT send patch, got %d patches", n)
	}

	// Unpin one, the 4th should now succeed.
	if err := cs.Pin(ctx, chats[0], false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if err := cs.Pin(ctx, fourth, true); err != nil {
		t.Fatalf("Pin after unpin: %v", err)
	}
}

// TestMarkUnreadIdempotent verifies two MarkUnread calls both succeed and
// each pushes an appstate patch (whatsmeow handles idempotency server-side).
func TestMarkUnreadIdempotent(t *testing.T) {
	cs, fc := newTestChatState(t, time.Now)
	chat := domain.MustJID("5511999999999")
	ctx := context.Background()

	if err := cs.MarkUnread(ctx, chat); err != nil {
		t.Fatalf("MarkUnread #1: %v", err)
	}
	if err := cs.MarkUnread(ctx, chat); err != nil {
		t.Fatalf("MarkUnread #2: %v", err)
	}
	if n := len(fc.AppStatePatches); n != 2 {
		t.Fatalf("want 2 appstate patches, got %d", n)
	}
}
