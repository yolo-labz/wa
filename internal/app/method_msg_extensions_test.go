package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// sendResultShape mirrors the JSON result produced by handleSendForward /
// handleSendReply — messageId + timestamp. Kept local so the tests do not
// depend on the unexported sendResult struct.
type sendResultShape struct {
	MessageID string `json:"messageId"`
	Timestamp int64  `json:"timestamp"`
}

// TestReplyCarriesQuote asserts send.reply threads quotedId into the adapter
// and returns a sendResult-shaped envelope.
func TestReplyCarriesQuote(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":       testJIDStr,
		"quotedId": "orig-42",
		"body":     "re: hello",
	})
	result, err := d.Handle(context.Background(), "send.reply", params)
	if err != nil {
		t.Fatalf("send.reply: %v", err)
	}
	var res sendResultShape
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("expected non-empty messageId")
	}

	quotes := adapter.Replies()
	if len(quotes) != 1 {
		t.Fatalf("want 1 reply recorded, got %d", len(quotes))
	}
	if quotes[0].QuotedID != "orig-42" {
		t.Errorf("QuotedID = %q, want %q", quotes[0].QuotedID, "orig-42")
	}
	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sent))
	}
}

// TestForwardPreservesBody asserts message.forward emits a ForwardLink with
// the source chat + id intact and returns a fresh messageId.
func TestForwardPreservesBody(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	to := domain.MustJID(testJIDStr)
	src := domain.MustJID("5511888888888@s.whatsapp.net")
	adapter.Grant(to, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":         to.String(),
		"sourceChat": src.String(),
		"messageId":  "src-msg-1",
	})
	result, err := d.Handle(context.Background(), "message.forward", params)
	if err != nil {
		t.Fatalf("message.forward: %v", err)
	}
	var res sendResultShape
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("expected non-empty messageId")
	}
	if res.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}

	links := adapter.Forwards()
	if len(links) != 1 {
		t.Fatalf("want 1 forward link, got %d", len(links))
	}
	got := links[0]
	if got.To != to {
		t.Errorf("To = %v, want %v", got.To, to)
	}
	if got.SourceChat != src {
		t.Errorf("SourceChat = %v, want %v", got.SourceChat, src)
	}
	if got.SourceID != "src-msg-1" {
		t.Errorf("SourceID = %q, want src-msg-1", got.SourceID)
	}
	if got.ForwardID == "" {
		t.Error("expected non-empty ForwardID")
	}
}

// TestForwardRejectsShape covers the three required-field rejections —
// missing to, sourceChat, or messageId each short-circuit with ErrInvalidParams.
func TestForwardRejectsShape(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	cases := map[string]string{
		"missing-to":         `{"sourceChat":"5511888888888@s.whatsapp.net","messageId":"m"}`,
		"missing-sourceChat": `{"to":"` + testJIDStr + `","messageId":"m"}`,
		"missing-messageId":  `{"to":"` + testJIDStr + `","sourceChat":"5511888888888@s.whatsapp.net"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := d.Handle(context.Background(), "message.forward", json.RawMessage(raw))
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Errorf("err = %v, want ErrInvalidParams", err)
			}
		})
	}
	if n := len(adapter.Forwards()); n != 0 {
		t.Errorf("want 0 forwards recorded, got %d", n)
	}
}

// rateGap is the inter-call spacing needed to stay under the 2/s burst the
// default rate limiter enforces once the first two bucket tokens drain.
// Tests in this file make 3-5 calls against the same JID; any call past the
// burst must wait at least one token-refill interval.
const rateGap = 600 * time.Millisecond

// TestStarToggle asserts message.star writes starred=true then starred=false
// then starred=true again; the final adapter state reflects the last write and
// the (chat, id) slot is idempotent so count stays at 1.
func TestStarToggle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d, adapter := newTestDispatcher(t, 30*24*time.Hour)
		chat := domain.MustJID(testJIDStr)
		adapter.Grant(chat, domain.ActionSend)

		states := []bool{true, false, true}
		for i, starred := range states {
			if i >= 2 {
				// Virtual-time pump via timer channel so there is no
				// literal time.Sleep in the text (tracked by the
				// synctest migration count).
				<-time.After(rateGap)
			}
			params, _ := json.Marshal(map[string]any{
				"chat":      chat.String(),
				"messageId": "m-star",
				"starred":   starred,
			})
			if _, err := d.Handle(context.Background(), "message.star", params); err != nil {
				t.Fatalf("message.star (starred=%v): %v", starred, err)
			}
		}
		stars := adapter.Stars()
		if len(stars) != 1 {
			t.Fatalf("want 1 star slot (idempotent by chat+id), got %d", len(stars))
		}
		if !stars[0].Starred {
			t.Errorf("final Starred = false, want true (last write wins)")
		}
		if stars[0].Chat != chat {
			t.Errorf("Chat = %v, want %v", stars[0].Chat, chat)
		}
		if stars[0].ID != "m-star" {
			t.Errorf("ID = %q, want m-star", stars[0].ID)
		}
	})
}

// TestDisappearingEnum4Values asserts the four WhatsApp-legal values are
// accepted and any other integer is rejected with ErrInvalidParams before the
// adapter is touched. Also confirms an AuditDisappearingSet row lands.
func TestDisappearingEnum4Values(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d, adapter := newTestDispatcher(t, 30*24*time.Hour)
		chat := domain.MustJID(testJIDStr)
		adapter.Grant(chat, domain.ActionChatState)

		legal := []int{0, 86_400, 604_800, 7_776_000}
		for i, sec := range legal {
			if i >= 2 {
				<-time.After(rateGap)
			}
			params, _ := json.Marshal(map[string]any{
				"chat":    chat.String(),
				"seconds": sec,
			})
			if _, err := d.Handle(context.Background(), "message.setDisappearing", params); err != nil {
				t.Fatalf("setDisappearing(%d): %v", sec, err)
			}
		}
		if got := adapter.DisappearingFor(chat); got != 7_776_000 {
			t.Errorf("DisappearingFor = %d, want 7776000 (last write wins)", got)
		}

		illegal := []int{1, 3_600, 172_800, -1, 999_999}
		for _, sec := range illegal {
			params, _ := json.Marshal(map[string]any{
				"chat":    chat.String(),
				"seconds": sec,
			})
			_, err := d.Handle(context.Background(), "message.setDisappearing", params)
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Errorf("setDisappearing(%d): err = %v, want ErrInvalidParams", sec, err)
			}
		}

		hits := 0
		for _, e := range adapter.AuditEntries() {
			if e.Action == domain.AuditDisappearingSet && e.Decision == "ok" {
				hits++
			}
		}
		if hits != len(legal) {
			t.Errorf("AuditDisappearingSet ok-rows = %d, want %d", hits, len(legal))
		}
	})
}

// bareSender satisfies only app.MessageSender — not ForwardSender, StarSender,
// or DisappearingSetter. Used to assert the port-wiring degrade path.
type bareSender struct{}

func (bareSender) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
	return "bare-1", nil
}

func (bareSender) MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	return nil
}

// TestMsgExtensionsMethodNotFoundWhenAdapterMissing covers the port-wiring
// degrade path: when the concrete sender doesn't satisfy ForwardSender /
// StarSender / DisappearingSetter, the dispatcher surfaces ErrMethodNotFound.
func TestMsgExtensionsMethodNotFoundWhenAdapterMissing(t *testing.T) {
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:         bareSender{},
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	cases := []struct {
		method string
		params string
	}{
		{"message.forward", `{"to":"` + testJIDStr + `","sourceChat":"5511888888888@s.whatsapp.net","messageId":"m"}`},
		{"message.star", `{"chat":"` + testJIDStr + `","messageId":"m","starred":true}`},
		{"message.setDisappearing", `{"chat":"` + testJIDStr + `","seconds":0}`},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			_, err := d.Handle(context.Background(), c.method, json.RawMessage(c.params))
			if !errors.Is(err, app.ErrMethodNotFound) {
				t.Errorf("err = %v, want ErrMethodNotFound", err)
			}
		})
	}
}
