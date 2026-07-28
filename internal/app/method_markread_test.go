package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func markReadParams(t *testing.T, chat, messageID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"chat": chat, "messageId": messageID})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

// receiptRecorder wraps a MessageSender and records which of the two
// receipt methods the dispatcher picked. The memory adapter answers both
// with a bare nil, so nothing downstream can tell them apart otherwise.
type receiptRecorder struct {
	app.MessageSender
	calls []string
}

func (r *receiptRecorder) MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	r.calls = append(r.calls, "read")
	return r.MessageSender.MarkRead(ctx, chat, id)
}

func (r *receiptRecorder) MarkPlayed(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	r.calls = append(r.calls, "played")
	return r.MessageSender.MarkPlayed(ctx, chat, id)
}

// newRecordingDispatcher wires a receiptRecorder over the memory adapter
// and grants the read action, which both receipt kinds require.
func newRecordingDispatcher(t *testing.T) (*app.Dispatcher, *receiptRecorder) {
	t.Helper()
	rec := &receiptRecorder{}
	d, adapter := newTestDispatcherWith(t, 30*24*time.Hour, func(s app.MessageSender) app.MessageSender {
		rec.MessageSender = s
		return rec
	})
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionRead)
	return d, rec
}

// TestMarkRead_PlayedSelectsTheStrongerReceipt pins the routing the
// "played" param exists for. A voice note acknowledged with a plain read
// receipt looks unlistened-to on the sender's phone, so picking the
// wrong branch is silently wrong rather than an error — only asserting
// which port method fired can catch it.
func TestMarkRead_PlayedSelectsTheStrongerReceipt(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"played_omitted", `{"chat":"` + testJIDStr + `","messageId":"MSG-1"}`, "read"},
		{"played_false", `{"chat":"` + testJIDStr + `","messageId":"MSG-2","played":false}`, "read"},
		{"played_true", `{"chat":"` + testJIDStr + `","messageId":"MSG-3","played":true}`, "played"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, rec := newRecordingDispatcher(t)

			result, err := d.Handle(context.Background(), "markRead", json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("Handle(markRead): %v", err)
			}
			if string(result) != "{}" {
				t.Errorf("result = %s, want {}", result)
			}
			if len(rec.calls) != 1 || rec.calls[0] != tc.want {
				t.Fatalf("port calls = %v, want [%s]", rec.calls, tc.want)
			}
		})
	}
}

// TestMarkRead_PlayedStillNeedsTheReadGrant: "played" is a stronger
// receipt, not a different permission. It must not become a way around
// the allowlist for a chat that was never granted the read action.
func TestMarkRead_PlayedStillNeedsTheReadGrant(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)

	raw := json.RawMessage(`{"chat":"` + testJIDStr + `","messageId":"MSG-1","played":true}`)
	_, err := d.Handle(context.Background(), "markRead", raw)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "denied:allowlist" {
		t.Fatalf("audit = %+v, want one denied:allowlist entry", entries)
	}
}

// TestMarkRead_Succeeds pins FR-008: an allowlisted read-action chat
// marks read and audits "ok".
func TestMarkRead_Succeeds(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionRead)

	result, err := d.Handle(context.Background(), "markRead", markReadParams(t, testJIDStr, "MSG-1"))
	if err != nil {
		t.Fatalf("Handle(markRead): %v", err)
	}
	if string(result) != "{}" {
		t.Errorf("result = %s, want {}", result)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "ok" {
		t.Fatalf("audit = %+v, want one ok entry", entries)
	}
}

// TestMarkRead_DeniedNotAllowlisted pins FR-009: without a read grant
// the safety pipeline refuses before the sender port is reached.
func TestMarkRead_DeniedNotAllowlisted(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)

	_, err := d.Handle(context.Background(), "markRead", markReadParams(t, testJIDStr, "MSG-1"))
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "denied:allowlist" {
		t.Fatalf("audit = %+v, want one denied:allowlist entry", entries)
	}
}

// TestMarkRead_MissingParams pins the param contract: chat and
// messageId are both mandatory.
func TestMarkRead_MissingParams(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	cases := []struct {
		name string
		raw  string
	}{
		{"empty_object", `{}`},
		{"missing_message_id", `{"chat":"` + testJIDStr + `"}`},
		{"missing_chat", `{"messageId":"MSG-1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Handle(context.Background(), "markRead", json.RawMessage(tc.raw))
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Errorf("err = %v, want ErrInvalidParams", err)
			}
		})
	}
}

// TestMarkRead_BadJID pins that an unparseable chat JID maps to
// ErrInvalidJID, not a sender error.
func TestMarkRead_BadJID(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	_, err := d.Handle(context.Background(), "markRead", markReadParams(t, "not a jid", "MSG-1"))
	if !errors.Is(err, app.ErrInvalidJID) {
		t.Fatalf("err = %v, want ErrInvalidJID", err)
	}
}
