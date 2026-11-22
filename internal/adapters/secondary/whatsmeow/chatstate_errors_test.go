package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestChatState_RefusalsSendNothing: every ChatStateAdapter operation
// refuses a cancelled context, the zero JID and a disconnected client with
// the typed error, and never pushes an appstate patch while refusing.
func TestChatState_RefusalsSendNothing(t *testing.T) {
	t.Parallel()
	chat := domain.MustJID("5511999999999")
	ops := map[string]func(context.Context, *ChatStateAdapter, domain.JID) error{
		"Archive":    func(ctx context.Context, c *ChatStateAdapter, j domain.JID) error { return c.Archive(ctx, j, true) },
		"Mute":       func(ctx context.Context, c *ChatStateAdapter, j domain.JID) error { return c.Mute(ctx, j, nil) },
		"Pin":        func(ctx context.Context, c *ChatStateAdapter, j domain.JID) error { return c.Pin(ctx, j, true) },
		"MarkUnread": func(ctx context.Context, c *ChatStateAdapter, j domain.JID) error { return c.MarkUnread(ctx, j) },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cs, fc := newTestChatState(t, time.Now)

			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := op(cancelled, cs, chat); !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled ctx: err = %v, want context.Canceled", err)
			}
			if err := op(context.Background(), cs, domain.JID{}); !errors.Is(err, domain.ErrInvalidJID) {
				t.Errorf("zero JID: err = %v, want ErrInvalidJID", err)
			}
			fc.mu.Lock()
			fc.ConnectedFlag = false
			fc.mu.Unlock()
			if err := op(context.Background(), cs, chat); !errors.Is(err, domain.ErrDisconnected) {
				t.Errorf("disconnected: err = %v, want ErrDisconnected", err)
			}
			if n := len(fc.AppStatePatches); n != 0 {
				t.Errorf("%d appstate patches sent while refusing, want 0", n)
			}
		})
	}
}

// TestChatState_UpstreamFailureIsWrapped: a SendAppState failure reaches the
// caller wrapped (errors.Is finds the cause) for every operation.
func TestChatState_UpstreamFailureIsWrapped(t *testing.T) {
	t.Parallel()
	boom := errors.New("appstate rejected")
	chat := domain.MustJID("5511999999999")
	ctx := context.Background()
	cs, fc := newTestChatState(t, time.Now)
	fc.AppStateErr = boom

	until := time.Now().Add(time.Hour)
	for name, err := range map[string]error{
		"Archive":    cs.Archive(ctx, chat, true),
		"Mute":       cs.Mute(ctx, chat, &until),
		"Pin":        cs.Pin(ctx, chat, true),
		"MarkUnread": cs.MarkUnread(ctx, chat),
	} {
		if !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the upstream cause wrapped", name, err)
		}
	}
}

// TestChatState_PinCapBookkeeping: a failed pin does not take a slot, an
// unpin frees one, and re-pinning an already pinned chat at the cap is not
// refused. Mutating the bookkeeping (count before send, never delete,
// refuse re-pin) breaks one of the assertions.
func TestChatState_PinCapBookkeeping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs, fc := newTestChatState(t, time.Now)
	jid := func(n string) domain.JID { return domain.MustJID("55119999999" + n) }

	fc.AppStateErr = errors.New("transient")
	for i := range maxPinnedChats + 1 {
		if err := cs.Pin(ctx, jid(string(rune('0'+i))), true); errors.Is(err, domain.ErrUpstreamError) {
			t.Fatalf("failed pin #%d consumed a slot: refused with the cap error", i)
		}
	}
	fc.AppStateErr = nil

	for i := range maxPinnedChats {
		if err := cs.Pin(ctx, jid(string(rune('0'+i))), true); err != nil {
			t.Fatalf("pin #%d: %v", i, err)
		}
	}
	if err := cs.Pin(ctx, jid("7"), true); !errors.Is(err, domain.ErrUpstreamError) {
		t.Fatalf("pin over the cap: err = %v, want ErrUpstreamError", err)
	}
	if err := cs.Pin(ctx, jid("0"), true); err != nil {
		t.Fatalf("re-pin of a pinned chat at the cap: %v", err)
	}
	if err := cs.Pin(ctx, jid("0"), false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if err := cs.Pin(ctx, jid("7"), true); err != nil {
		t.Fatalf("pin after an unpin freed a slot: %v", err)
	}
}

// TestChatState_MuteRejectsPastBeforeConnectivity: a past timestamp is a
// caller error and wins over a disconnected client, and sends nothing.
func TestChatState_MuteRejectsPastBeforeConnectivity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cs, fc := newTestChatState(t, func() time.Time { return now })
	fc.ConnectedFlag = false

	for _, until := range []time.Time{now, now.Add(-time.Minute)} {
		if err := cs.Mute(context.Background(), domain.MustJID("5511999999999"), &until); !errors.Is(err, domain.ErrPastMuteTimestamp) {
			t.Errorf("until=%s: err = %v, want ErrPastMuteTimestamp", until, err)
		}
	}
	if len(fc.AppStatePatches) != 0 {
		t.Error("a refused mute sent a patch")
	}
}
