package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

func newPresenceDispatcher(t *testing.T) (*app.Dispatcher, *memory.Adapter, *memory.FakeClock) {
	t.Helper()
	clk := &memory.FakeClock{T: time.Unix(1_700_000_000, 0).UTC()}
	adapter := memory.New(clk)
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
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
	return d, adapter, clk
}

// TestPresenceComposingRoundTrip verifies that the start/stop pair reaches
// the adapter with the expected wire states: "composing" for start and
// "paused" for stop (whatsmeow collapses both -stop methods onto the single
// `paused` upstream frame per FR-032).
func TestPresenceComposingRoundTrip(t *testing.T) {
	d, adapter, clk := newPresenceDispatcher(t)
	ctx := context.Background()
	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})

	if _, err := d.Handle(ctx, "presence.composing.start", params); err != nil {
		t.Fatalf("composing.start: %v", err)
	}
	// Advance the adapter clock past the 1/s/chat window so .stop is not
	// silently dropped as rate-limited excess.
	clk.Advance(2 * time.Second)
	if _, err := d.Handle(ctx, "presence.composing.stop", params); err != nil {
		t.Fatalf("composing.stop: %v", err)
	}
	emissions := adapter.ComposingEmissions()
	if len(emissions) != 1 {
		t.Fatalf("want 1 chat emission, got %d", len(emissions))
	}
	em := emissions[0]
	if em.Accepted != 2 {
		t.Errorf("accepted = %d, want 2", em.Accepted)
	}
	if em.LastState != "paused" {
		t.Errorf("last state = %q, want paused", em.LastState)
	}
}

// TestPresenceRecordingRoundTrip verifies the recording pair: start emits
// "recording", stop emits "paused" (shared with composing.stop at the wire).
func TestPresenceRecordingRoundTrip(t *testing.T) {
	d, adapter, clk := newPresenceDispatcher(t)
	ctx := context.Background()
	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})

	if _, err := d.Handle(ctx, "presence.recording.start", params); err != nil {
		t.Fatalf("recording.start: %v", err)
	}
	// Read-back the per-chat state after .start landed so we can assert the
	// transition to "paused" is detected regardless of the clock.
	first := adapter.ComposingEmissions()
	if len(first) != 1 || first[0].LastState != "recording" {
		t.Fatalf("after start: got %+v, want one emission with state=recording", first)
	}

	clk.Advance(2 * time.Second)
	if _, err := d.Handle(ctx, "presence.recording.stop", params); err != nil {
		t.Fatalf("recording.stop: %v", err)
	}
	em := adapter.ComposingEmissions()[0]
	if em.Accepted != 2 {
		t.Errorf("accepted = %d, want 2", em.Accepted)
	}
	if em.LastState != "paused" {
		t.Errorf("last state = %q, want paused", em.LastState)
	}
}

// TestPresenceRejectsEmptyChat verifies shape validation short-circuits
// ahead of the adapter — empty chat yields -32602, zero adapter hits.
func TestPresenceRejectsEmptyChat(t *testing.T) {
	d, adapter, _ := newPresenceDispatcher(t)
	params, _ := json.Marshal(map[string]string{"chat": ""})

	for _, method := range []string{
		"presence.composing.start",
		"presence.composing.stop",
		"presence.recording.start",
		"presence.recording.stop",
	} {
		_, err := d.Handle(context.Background(), method, params)
		if !errors.Is(err, app.ErrInvalidParams) {
			t.Errorf("%s: err = %v, want ErrInvalidParams", method, err)
		}
	}
	if n := len(adapter.ComposingEmissions()); n != 0 {
		t.Errorf("want 0 emissions after shape rejections, got %d", n)
	}
}

// TestPresenceAbsentReturnsMethodNotFound verifies that when Presence is
// not wired, all four presence.* methods degrade to ErrMethodNotFound.
func TestPresenceAbsentReturnsMethodNotFound(t *testing.T) {
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
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})
	for _, method := range []string{
		"presence.composing.start",
		"presence.composing.stop",
		"presence.recording.start",
		"presence.recording.stop",
	} {
		_, err := d.Handle(context.Background(), method, params)
		if !errors.Is(err, app.ErrMethodNotFound) {
			t.Errorf("%s: err = %v, want ErrMethodNotFound", method, err)
		}
	}
}
