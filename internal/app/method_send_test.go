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

const testJIDStr = "5511999999999@s.whatsapp.net"

func newTestDispatcher(t *testing.T, sessionAge time.Duration) (*app.Dispatcher, *memory.Adapter) {
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
		SessionCreated: time.Now().Add(-sessionAge),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// T021: send succeeds with allowlisted JID.
func TestSendSucceeds(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour) // mature session
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})
	result, err := d.Handle(context.Background(), "send", params)
	if err != nil {
		t.Fatalf("Handle(send): %v", err)
	}

	var res struct {
		MessageID string `json:"messageId"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.MessageID == "" {
		t.Error("expected non-empty messageId")
	}
	if res.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}

	// Verify exactly 1 message was sent.
	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}

	// Verify audit entry with "ok".
	entries := adapter.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Decision != "ok" {
		t.Errorf("expected audit decision 'ok', got %q", entries[0].Decision)
	}
}

// T022: send denied by allowlist.
func TestSendDeniedByAllowlist(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	// Do NOT grant the JID.

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})
	_, err := d.Handle(context.Background(), "send", params)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("expected ErrNotAllowlisted, got %v", err)
	}

	// Verify no Send call was made.
	if len(adapter.Sent()) != 0 {
		t.Error("expected 0 sent messages")
	}

	// Verify audit entry with "denied:allowlist".
	entries := adapter.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Decision != "denied:allowlist" {
		t.Errorf("expected audit decision 'denied:allowlist', got %q", entries[0].Decision)
	}
}

// T023: send denied by rate limiter.
func TestSendDeniedByRateLimiter(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour) // mature = full rate caps
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})

	// Exhaust the per-second burst (2 at full rate).
	for i := 0; i < 2; i++ {
		_, err := d.Handle(context.Background(), "send", params)
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	// The 3rd send within the same second should be rate-limited.
	_, err := d.Handle(context.Background(), "send", params)
	if !errors.Is(err, app.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

// T024: send denied by warmup.
func TestSendDeniedByWarmup(t *testing.T) {
	// 3-day-old session → 25% warmup → burst 1 per second.
	d, adapter := newTestDispatcher(t, 3*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})

	// First send should succeed (burst 1).
	_, err := d.Handle(context.Background(), "send", params)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	// Second send within the same instant should be rejected with warmup.
	_, err = d.Handle(context.Background(), "send", params)
	if !errors.Is(err, app.ErrWarmupActive) {
		t.Fatalf("expected ErrWarmupActive, got %v", err)
	}
}

// T025: sendMedia and react go through same pipeline.
func TestSendMediaAndReact(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	// sendMedia
	mediaParams, _ := json.Marshal(map[string]string{
		"to":   testJIDStr,
		"path": "/tmp/test.jpg",
		"mime": "image/jpeg",
	})
	result, err := d.Handle(context.Background(), "sendMedia", mediaParams)
	if err != nil {
		t.Fatalf("Handle(sendMedia): %v", err)
	}
	var res struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("sendMedia: expected non-empty messageId")
	}

	// react
	reactParams, _ := json.Marshal(map[string]string{
		"chat":      testJIDStr,
		"messageId": "msg-123",
		"emoji":     "👍",
	})
	reactResult, err := d.Handle(context.Background(), "react", reactParams)
	if err != nil {
		t.Fatalf("Handle(react): %v", err)
	}
	// react returns {}
	var empty struct{}
	if err := json.Unmarshal(reactResult, &empty); err != nil {
		t.Fatalf("unmarshal react result: %v", err)
	}

	// Verify both went through: 2 sent messages, 2 audit entries.
	if len(adapter.Sent()) != 2 {
		t.Errorf("expected 2 sent messages, got %d", len(adapter.Sent()))
	}
	if len(adapter.AuditEntries()) != 2 {
		t.Errorf("expected 2 audit entries, got %d", len(adapter.AuditEntries()))
	}

	// Verify react is denied for non-allowlisted JID.
	adapter.Revoke(jid, domain.ActionSend)
	_, err = d.Handle(context.Background(), "react", reactParams)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("react: expected ErrNotAllowlisted, got %v", err)
	}
}

// T026: nil/empty params returns ErrInvalidParams.
func TestSendNilParams(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	tests := []struct {
		name   string
		method string
		params json.RawMessage
	}{
		{"send nil", "send", nil},
		{"send empty", "send", json.RawMessage{}},
		{"sendMedia nil", "sendMedia", nil},
		{"react nil", "react", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.Handle(context.Background(), tt.method, tt.params)
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Errorf("expected ErrInvalidParams, got %v", err)
			}
		})
	}
}

// Extra: unknown method returns ErrMethodNotFound.
func TestUnknownMethod(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)
	_, err := d.Handle(context.Background(), "nosuchmethod", nil)
	if !errors.Is(err, app.ErrMethodNotFound) {
		t.Errorf("expected ErrMethodNotFound, got %v", err)
	}
}
