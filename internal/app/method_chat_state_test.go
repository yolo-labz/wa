package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// chatStateRec records the chat-state calls the dispatcher forwards.
type chatStateRec struct {
	calls []string
	until *time.Time
	err   error
}

func (c *chatStateRec) Archive(_ context.Context, _ domain.JID, v bool) error {
	c.calls = append(c.calls, fmt.Sprintf("archive:%v", v))
	return c.err
}

func (c *chatStateRec) Mute(_ context.Context, _ domain.JID, until *time.Time) error {
	c.calls = append(c.calls, "mute")
	c.until = until
	return c.err
}

func (c *chatStateRec) Pin(_ context.Context, _ domain.JID, v bool) error {
	c.calls = append(c.calls, fmt.Sprintf("pin:%v", v))
	return c.err
}

func (c *chatStateRec) MarkUnread(context.Context, domain.JID) error {
	c.calls = append(c.calls, "unread")
	return c.err
}

func chatStateDispatcher(t *testing.T, cs app.ChatStateManager) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: adapter, Events: adapter, Contacts: adapter, Groups: adapter, Session: adapter,
		Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter, Quoted: adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		Websocket:      &fakeProbe{up: true},
		ChatState:      cs,
	})
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

func TestChatState_ForwardsEachMethod(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		params map[string]any
		want   string
	}{
		{"chat.archive", map[string]any{"chat": testJIDStr, "archived": true}, "archive:true"},
		{"chat.mute", map[string]any{"chat": testJIDStr, "untilUnix": 1893456000}, "mute"},
		{"chat.pin", map[string]any{"chat": testJIDStr, "pinned": true}, "pin:true"},
		{"chat.markUnread", map[string]any{"chat": testJIDStr}, "unread"},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			t.Parallel()
			rec := &chatStateRec{}
			d, adapter := chatStateDispatcher(t, rec)
			adapter.Grant(domain.MustJID(testJIDStr), domain.ActionChatState)
			raw, _ := json.Marshal(c.params)
			if _, err := d.Handle(context.Background(), c.method, raw); err != nil {
				t.Fatalf("Handle(%s): %v", c.method, err)
			}
			if len(rec.calls) != 1 || rec.calls[0] != c.want {
				t.Errorf("calls = %v, want [%s]", rec.calls, c.want)
			}
			if c.method == "chat.mute" && (rec.until == nil || rec.until.Unix() != 1893456000) {
				t.Errorf("mute deadline = %v, want 1893456000", rec.until)
			}
		})
	}
}

func TestChatState_RefusesBadInput(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"chat.archive", "chat.mute", "chat.pin", "chat.markUnread"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			rec := &chatStateRec{}
			d, adapter := chatStateDispatcher(t, rec)
			adapter.Grant(domain.MustJID(testJIDStr), domain.ActionChatState)
			for name, tc := range map[string]struct {
				params string
				want   error
			}{
				"no chat":     {`{}`, app.ErrInvalidParams},
				"bad jid":     {`{"chat":"not a jid"}`, app.ErrInvalidJID},
				"bad params":  {`{"chat":`, nil},
				"not granted": {`{"chat":"5511888888888@s.whatsapp.net"}`, nil},
			} {
				_, err := d.Handle(context.Background(), method, json.RawMessage(tc.params))
				if err == nil {
					t.Errorf("%s: no error", name)
				} else if tc.want != nil && !errors.Is(err, tc.want) {
					t.Errorf("%s: err = %v, want %v", name, err, tc.want)
				}
			}
			if len(rec.calls) != 0 {
				t.Errorf("a refused call reached the port: %v", rec.calls)
			}
		})
	}
}

func TestChatState_PortErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		typed   error
		wrapped bool
	}{
		{"past mute stays typed", domain.ErrPastMuteTimestamp, domain.ErrPastMuteTimestamp, false},
		{"upstream stays typed", domain.ErrUpstreamError, domain.ErrUpstreamError, false},
		{"disconnected stays typed", domain.ErrDisconnected, domain.ErrDisconnected, false},
		{"other gets the method name", errors.New("boom"), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, adapter := chatStateDispatcher(t, &chatStateRec{err: tc.err})
			adapter.Grant(domain.MustJID(testJIDStr), domain.ActionChatState)
			raw, _ := json.Marshal(map[string]any{"chat": testJIDStr, "pinned": true})
			_, err := d.Handle(context.Background(), "chat.pin", raw)
			if err == nil {
				t.Fatal("no error")
			}
			if tc.typed != nil && !errors.Is(err, tc.typed) {
				t.Errorf("err = %v, want %v", err, tc.typed)
			}
			if tc.wrapped && err.Error() != "chat.pin: boom" {
				t.Errorf("err = %q, want %q", err, "chat.pin: boom")
			}
		})
	}
}

func TestChatState_NoPortIsMethodNotFound(t *testing.T) {
	t.Parallel()
	d, adapter := chatStateDispatcher(t, nil)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionChatState)
	raw, _ := json.Marshal(map[string]any{"chat": testJIDStr})
	for _, m := range []string{"chat.archive", "chat.mute", "chat.pin", "chat.markUnread"} {
		if _, err := d.Handle(context.Background(), m, raw); err == nil {
			t.Errorf("%s with no port: no error", m)
		}
	}
}
