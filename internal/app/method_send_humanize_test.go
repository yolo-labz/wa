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

// newHumanizeE2EDispatcher wires the memory adapter as PresenceSender too,
// so the humanize flow's frames are observable via ComposingEmissions.
// Uses the real clock + the production delay model on purpose: the test
// asserts the end-to-end contract that the trailing paused frame clears
// the adapter's 1/s/chat budget (delay floor > 1 s).
func newHumanizeE2EDispatcher(t *testing.T) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		Presence:       adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// TestSendHumanizeEmitsTypingFlow drives the full JSON-RPC path: a send
// with humanize=true emits composing → (delay) → paused at the presence
// port, then exactly one message. Wall-clock cost is one production
// humanize delay (~1.2–2 s) — accepted for the single e2e proof.
func TestSendHumanizeEmitsTypingFlow(t *testing.T) {
	t.Parallel()
	d, adapter := newHumanizeE2EDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]any{"to": testJIDStr, "body": "hi", "humanize": true})
	if _, err := d.Handle(context.Background(), "send", params); err != nil {
		t.Fatalf("Handle(send humanize): %v", err)
	}

	if sent := adapter.Sent(); len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	emissions := adapter.ComposingEmissions()
	if len(emissions) != 1 {
		t.Fatalf("presence chats = %d, want 1", len(emissions))
	}
	em := emissions[0]
	if em.Accepted != 2 || em.Dropped != 0 {
		t.Errorf("presence accepted=%d dropped=%d, want 2/0 — paused frame must clear the 1/s budget", em.Accepted, em.Dropped)
	}
	if em.LastState != "paused" {
		t.Errorf("last presence state = %q, want paused", em.LastState)
	}
}

// TestSendWithoutHumanizeSkipsPresence verifies the flow is strictly
// opt-in: a plain send never touches the presence port.
func TestSendWithoutHumanizeSkipsPresence(t *testing.T) {
	t.Parallel()
	d, adapter := newHumanizeE2EDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]any{"to": testJIDStr, "body": "hi"})
	if _, err := d.Handle(context.Background(), "send", params); err != nil {
		t.Fatalf("Handle(send): %v", err)
	}
	if n := len(adapter.ComposingEmissions()); n != 0 {
		t.Errorf("presence emissions = %d, want 0 without humanize", n)
	}
}

// TestSendHumanizeDeniedLeaksNoPresence verifies gate ordering: a send
// refused by the allowlist must not leak a typing indicator to the target.
func TestSendHumanizeDeniedLeaksNoPresence(t *testing.T) {
	t.Parallel()
	d, adapter := newHumanizeE2EDispatcher(t)
	// No Grant — allowlist refuses.

	params, _ := json.Marshal(map[string]any{"to": testJIDStr, "body": "hi", "humanize": true})
	_, err := d.Handle(context.Background(), "send", params)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}
	if n := len(adapter.ComposingEmissions()); n != 0 {
		t.Errorf("presence emissions = %d, want 0 — denied send leaked a typing indicator", n)
	}
	if n := len(adapter.Sent()); n != 0 {
		t.Errorf("sent = %d, want 0", n)
	}
}
