package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// sendOnlySender hides every adapter method except the MessageSender
// surface, so the ReplySender type assertion in send.reply fails and
// the not-wired branch is reachable.
type sendOnlySender struct{ inner app.MessageSender }

func (s sendOnlySender) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
	return s.inner.Send(ctx, msg)
}

func (s sendOnlySender) MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	return s.inner.MarkRead(ctx, chat, id)
}

func (s sendOnlySender) MarkPlayed(ctx context.Context, chat domain.JID, id domain.MessageID) error {
	return s.inner.MarkPlayed(ctx, chat, id)
}

// newTier2Dispatcher wires the memory adapter into every port plus the
// optional Presence port (newTestDispatcher leaves Presence unset, so
// chat.composing reports method-not-found there).
func newTier2Dispatcher(t *testing.T, sender app.MessageSender, withPresence bool) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	if sender == nil {
		sender = adapter
	}
	cfg := app.DispatcherConfig{
		Sender:         sender,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		Quoted:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	if withPresence {
		cfg.Presence = adapter
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// TestSendReply_Succeeds pins FR-070: an allowlisted reply goes through
// the ReplySender port, links the quoted id, and audits ok.
func TestSendReply_Succeeds(t *testing.T) {
	d, adapter := newTier2Dispatcher(t, nil, false)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to": testJIDStr, "quotedId": "Q-1", "body": "re: hello",
	})
	result, err := d.Handle(context.Background(), "send.reply", params)
	if err != nil {
		t.Fatalf("Handle(send.reply): %v", err)
	}
	var res struct {
		MessageID string `json:"messageId"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.MessageID == "" || res.Timestamp == 0 {
		t.Errorf("result = %+v, want non-empty messageId + timestamp", res)
	}

	replies := adapter.Replies()
	if len(replies) != 1 || string(replies[0].QuotedID) != "Q-1" {
		t.Fatalf("replies = %+v, want one link to Q-1", replies)
	}
	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "ok" {
		t.Fatalf("audit = %+v, want one ok entry", entries)
	}
}

// sendReplyTo issues a send.reply with the canonical "to/Q-1/re" params and
// returns the dispatcher error, shared by the not-wired and denied paths.
func sendReplyTo(t *testing.T, d *app.Dispatcher) error {
	t.Helper()
	params, _ := json.Marshal(map[string]string{
		"to": testJIDStr, "quotedId": "Q-1", "body": "re",
	})
	_, err := d.Handle(context.Background(), "send.reply", params)
	return err
}

// TestSendReply_NotWired pins the port-gate: a sender without the
// ReplySender extension yields method-not-found, before params are
// even validated.
func TestSendReply_NotWired(t *testing.T) {
	adapter := memory.New(nil)
	d, _ := newTier2Dispatcher(t, sendOnlySender{inner: adapter}, false)

	if err := sendReplyTo(t, d); !errors.Is(err, app.ErrMethodNotFound) {
		t.Fatalf("err = %v, want ErrMethodNotFound", err)
	}
}

// TestSendReply_ParamValidation pins the mandatory-field and JID
// checks ahead of the safety pipeline.
func TestSendReply_ParamValidation(t *testing.T) {
	d, _ := newTier2Dispatcher(t, nil, false)

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"missing_body", `{"to":"` + testJIDStr + `","quotedId":"Q-1"}`, app.ErrInvalidParams},
		{"missing_quoted", `{"to":"` + testJIDStr + `","body":"x"}`, app.ErrInvalidParams},
		{"missing_to", `{"quotedId":"Q-1","body":"x"}`, app.ErrInvalidParams},
		{"bad_jid", `{"to":"not a jid","quotedId":"Q-1","body":"x"}`, app.ErrInvalidJID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Handle(context.Background(), "send.reply", json.RawMessage(tc.raw))
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestSendReply_DeniedNotAllowlisted pins that send.reply runs the
// same safety pipeline as send.
func TestSendReply_DeniedNotAllowlisted(t *testing.T) {
	d, adapter := newTier2Dispatcher(t, nil, false)

	if err := sendReplyTo(t, d); !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}
	if n := len(adapter.Replies()); n != 0 {
		t.Errorf("replies = %d, want 0 (denied before port)", n)
	}
}

// TestComposing_Succeeds pins FR-071: a presence-wired dispatcher
// emits the typing state through the PresenceSender port.
func TestComposing_Succeeds(t *testing.T) {
	d, adapter := newTier2Dispatcher(t, nil, true)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionPresence)

	params, _ := json.Marshal(map[string]any{
		"chat": testJIDStr, "state": "composing",
	})
	result, err := d.Handle(context.Background(), "chat.composing", params)
	if err != nil {
		t.Fatalf("Handle(chat.composing): %v", err)
	}
	if string(result) != "{}" {
		t.Errorf("result = %s, want {}", result)
	}

	emissions := adapter.ComposingEmissions()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1", len(emissions))
	}
	if emissions[0].LastState != "composing" || emissions[0].Accepted != 1 {
		t.Errorf("emission = %+v, want composing accepted once", emissions[0])
	}
}

// TestComposing_NotWired pins that a dispatcher without the Presence
// port reports method-not-found.
func TestComposing_NotWired(t *testing.T) {
	d, _ := newTier2Dispatcher(t, nil, false)

	params, _ := json.Marshal(map[string]any{"chat": testJIDStr, "state": "composing"})
	_, err := d.Handle(context.Background(), "chat.composing", params)
	if !errors.Is(err, app.ErrMethodNotFound) {
		t.Fatalf("err = %v, want ErrMethodNotFound", err)
	}
}

// TestComposing_InvalidState pins the state enum: only composing,
// recording, and paused pass validation.
func TestComposing_InvalidState(t *testing.T) {
	d, _ := newTier2Dispatcher(t, nil, true)

	params, _ := json.Marshal(map[string]any{"chat": testJIDStr, "state": "shouting"})
	_, err := d.Handle(context.Background(), "chat.composing", params)
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
}

// TestGroupsGet_Succeeds pins FR-072: groups.get returns subject,
// participants, and admins for a seeded group.
func TestGroupsGet_Succeeds(t *testing.T) {
	d, adapter := newTier2Dispatcher(t, nil, false)
	member := domain.MustJID(testJIDStr)
	admin := domain.MustJID("5511888888888@s.whatsapp.net")
	groupJID := domain.MustJID("120363021033254949@g.us")
	adapter.SeedGroup(domain.Group{
		JID:          groupJID,
		Subject:      "ops",
		Participants: []domain.JID{member, admin},
		Admins:       []domain.JID{admin},
		FetchedAt:    time.Unix(1_781_000_000, 0),
	})

	params, _ := json.Marshal(map[string]string{"jid": groupJID.String()})
	result, err := d.Handle(context.Background(), "groups.get", params)
	if err != nil {
		t.Fatalf("Handle(groups.get): %v", err)
	}
	var res struct {
		JID          string   `json:"jid"`
		Subject      string   `json:"subject"`
		Participants []string `json:"participants"`
		Admins       []string `json:"admins"`
		FetchedAt    int64    `json:"fetchedAt"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	// FR-005a: the subject is attacker-controllable and must not leak raw;
	// it lives in the channel envelope (verified in firewall_views_test.go).
	if res.JID != groupJID.String() || res.Subject != "" {
		t.Errorf("result = %+v, want seeded jid + empty raw subject", res)
	}
	if len(res.Participants) != 2 || len(res.Admins) != 1 {
		t.Errorf("participants = %v admins = %v, want 2 + 1", res.Participants, res.Admins)
	}
	if res.FetchedAt != 1_781_000_000 {
		t.Errorf("fetchedAt = %d, want 1781000000", res.FetchedAt)
	}
}

// TestGroupsGet_RejectsNonGroupJID pins that a user JID is refused
// before the GroupManager port is consulted.
func TestGroupsGet_RejectsNonGroupJID(t *testing.T) {
	d, _ := newTier2Dispatcher(t, nil, false)

	params, _ := json.Marshal(map[string]string{"jid": testJIDStr})
	_, err := d.Handle(context.Background(), "groups.get", params)
	if !errors.Is(err, app.ErrInvalidJID) {
		t.Fatalf("err = %v, want ErrInvalidJID", err)
	}
}

// TestGroupsGet_MissingJID pins the mandatory-param check.
func TestGroupsGet_MissingJID(t *testing.T) {
	d, _ := newTier2Dispatcher(t, nil, false)

	_, err := d.Handle(context.Background(), "groups.get", json.RawMessage(`{}`))
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
}
