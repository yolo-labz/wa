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
