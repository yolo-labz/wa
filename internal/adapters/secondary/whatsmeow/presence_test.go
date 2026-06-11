package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

const presenceTestJID = "5511999999999@s.whatsapp.net"

// newTestPresenceAdapter builds a PresenceAdapter over a connected fake
// client with a mutable fake clock. Advance the clock by reassigning
// through the returned setter.
func newTestPresenceAdapter(t *testing.T) (*PresenceAdapter, *fakeWhatsmeowClient, func(d time.Duration)) {
	t.Helper()
	fake := &fakeWhatsmeowClient{ConnectedFlag: true}
	now := time.Unix(1_700_000_000, 0).UTC()
	p := &PresenceAdapter{
		client: fake,
		nowFn:  func() time.Time { return now },
		lastAt: make(map[domain.JID]time.Time),
	}
	return p, fake, func(d time.Duration) { now = now.Add(d) }
}

// TestPresenceWireMapping verifies the tri-state port strings map onto
// WhatsApp's two-tag wire model: composing→(composing,text),
// recording→(composing,audio), paused→(paused,text).
func TestPresenceWireMapping(t *testing.T) {
	p, fake, advance := newTestPresenceAdapter(t)
	chat := domain.MustJID(presenceTestJID)
	ctx := context.Background()

	cases := []struct {
		state     string
		wantState waTypes.ChatPresence
		wantMedia waTypes.ChatPresenceMedia
	}{
		{"composing", waTypes.ChatPresenceComposing, waTypes.ChatPresenceMediaText},
		{"recording", waTypes.ChatPresenceComposing, waTypes.ChatPresenceMediaAudio},
		{"paused", waTypes.ChatPresencePaused, waTypes.ChatPresenceMediaText},
	}
	for _, tc := range cases {
		if err := p.SendComposing(ctx, chat, tc.state, 0); err != nil {
			t.Fatalf("SendComposing(%s): %v", tc.state, err)
		}
		advance(2 * time.Second)
	}

	if len(fake.ChatPresenceCalls) != len(cases) {
		t.Fatalf("wire calls = %d, want %d", len(fake.ChatPresenceCalls), len(cases))
	}
	for i, tc := range cases {
		got := fake.ChatPresenceCalls[i]
		if got.State != tc.wantState || got.Media != tc.wantMedia {
			t.Errorf("%s: wire (state=%q media=%q), want (state=%q media=%q)",
				tc.state, got.State, got.Media, tc.wantState, tc.wantMedia)
		}
		if got.JID.User != "5511999999999" {
			t.Errorf("%s: wire JID user = %q", tc.state, got.JID.User)
		}
	}
}

// TestPresenceBudgetDropsExcess verifies the 1/s/chat budget: a second call
// inside the window is silently dropped (nil error, no wire call); a call
// after the window goes through.
func TestPresenceBudgetDropsExcess(t *testing.T) {
	p, fake, advance := newTestPresenceAdapter(t)
	chat := domain.MustJID(presenceTestJID)
	ctx := context.Background()

	if err := p.SendComposing(ctx, chat, "composing", 0); err != nil {
		t.Fatalf("first: %v", err)
	}
	advance(500 * time.Millisecond)
	if err := p.SendComposing(ctx, chat, "paused", 0); err != nil {
		t.Fatalf("in-window call must drop silently, got err: %v", err)
	}
	if len(fake.ChatPresenceCalls) != 1 {
		t.Fatalf("wire calls after in-window drop = %d, want 1", len(fake.ChatPresenceCalls))
	}

	advance(time.Second)
	if err := p.SendComposing(ctx, chat, "paused", 0); err != nil {
		t.Fatalf("post-window: %v", err)
	}
	if len(fake.ChatPresenceCalls) != 2 {
		t.Fatalf("wire calls after window elapsed = %d, want 2", len(fake.ChatPresenceCalls))
	}
}

// TestPresenceBudgetIsPerChat verifies two different chats inside the same
// second each get their own budget slot.
func TestPresenceBudgetIsPerChat(t *testing.T) {
	p, fake, _ := newTestPresenceAdapter(t)
	ctx := context.Background()

	if err := p.SendComposing(ctx, domain.MustJID(presenceTestJID), "composing", 0); err != nil {
		t.Fatalf("chat A: %v", err)
	}
	if err := p.SendComposing(ctx, domain.MustJID("5511888888888@s.whatsapp.net"), "composing", 0); err != nil {
		t.Fatalf("chat B: %v", err)
	}
	if len(fake.ChatPresenceCalls) != 2 {
		t.Fatalf("wire calls = %d, want 2 (per-chat budget)", len(fake.ChatPresenceCalls))
	}
}

// TestPresenceValidation covers the synchronous error surface: zero JID,
// unknown state, disconnected client, canceled context, upstream failure.
func TestPresenceValidation(t *testing.T) {
	ctx := context.Background()
	chat := domain.MustJID(presenceTestJID)

	t.Run("zero JID", func(t *testing.T) {
		p, _, _ := newTestPresenceAdapter(t)
		if err := p.SendComposing(ctx, domain.JID{}, "composing", 0); !errors.Is(err, domain.ErrInvalidJID) {
			t.Errorf("err = %v, want ErrInvalidJID", err)
		}
	})
	t.Run("unknown state", func(t *testing.T) {
		p, fake, _ := newTestPresenceAdapter(t)
		if err := p.SendComposing(ctx, chat, "typing", 0); err == nil {
			t.Error("want error for unknown state")
		}
		if len(fake.ChatPresenceCalls) != 0 {
			t.Errorf("unknown state must not reach the wire, got %d calls", len(fake.ChatPresenceCalls))
		}
	})
	t.Run("disconnected", func(t *testing.T) {
		p, fake, _ := newTestPresenceAdapter(t)
		fake.ConnectedFlag = false
		if err := p.SendComposing(ctx, chat, "composing", 0); !errors.Is(err, domain.ErrDisconnected) {
			t.Errorf("err = %v, want ErrDisconnected", err)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		p, _, _ := newTestPresenceAdapter(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := p.SendComposing(canceled, chat, "composing", 0); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("upstream error wrapped", func(t *testing.T) {
		p, fake, _ := newTestPresenceAdapter(t)
		sentinel := errors.New("boom")
		fake.ChatPresenceErr = sentinel
		if err := p.SendComposing(ctx, chat, "composing", 0); !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want wrapped sentinel", err)
		}
	})
}

// TestNewPresenceForNilClient verifies the constructor refuses a nil
// client so the composition root degrades at boot instead of panicking at
// first use.
func TestNewPresenceForNilClient(t *testing.T) {
	if _, err := (&Adapter{}).NewPresenceFor(); err == nil {
		t.Error("want error for nil client")
	}
	var nilAdapter *Adapter
	if _, err := nilAdapter.NewPresenceFor(); err == nil {
		t.Error("want error for nil adapter")
	}
}
