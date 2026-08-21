package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const testJIDStr = "5511999999999@s.whatsapp.net"

func newTestDispatcher(t *testing.T, sessionAge time.Duration) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	return newTestDispatcherWith(t, sessionAge, nil)
}

// newTestDispatcherWith is newTestDispatcher with a hook to decorate the
// sender port. wrap receives the memory adapter and returns whatever the
// dispatcher should call instead; nil means "use the adapter directly".
func newTestDispatcherWith(t *testing.T, sessionAge time.Duration, wrap func(app.MessageSender) app.MessageSender) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	var sender app.MessageSender = adapter
	if wrap != nil {
		sender = wrap(adapter)
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

// TestSendWithMentions — PR #292: a `send` with a `mentions` param builds a
// TextMessage carrying the parsed mention JIDs (bare number → user JID), and
// an unparseable mention fails the call with ErrInvalidJID before any send.
func TestSendWithMentions(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]any{
		"to":       testJIDStr,
		"body":     "bom dia",
		"mentions": []string{"5581999999999", "5581888888888@s.whatsapp.net"},
	})
	if _, err := d.Handle(context.Background(), "send", params); err != nil {
		t.Fatalf("Handle(send): %v", err)
	}

	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	tm, ok := sent[0].(domain.TextMessage)
	if !ok {
		t.Fatalf("sent[0] is %T, want domain.TextMessage", sent[0])
	}
	if len(tm.Mentions) != 2 {
		t.Fatalf("Mentions len = %d, want 2", len(tm.Mentions))
	}
	if tm.Mentions[0].String() != "5581999999999@s.whatsapp.net" {
		t.Errorf("Mentions[0] = %q, want bare number normalised to user JID", tm.Mentions[0].String())
	}

	// A malformed mention rejects the whole call with ErrInvalidJID.
	bad, _ := json.Marshal(map[string]any{
		"to":       testJIDStr,
		"body":     "oi",
		"mentions": []string{"not-a-jid!!"},
	})
	if _, err := d.Handle(context.Background(), "send", bad); !errors.Is(err, app.ErrInvalidJID) {
		t.Fatalf("bad mention: want ErrInvalidJID, got %v", err)
	}
	if len(adapter.Sent()) != 1 {
		t.Errorf("malformed mention must not send; got %d total", len(adapter.Sent()))
	}

	// A non-addressable mention (a group JID cannot be @mentioned) is rejected
	// early with ErrInvalidJID — before the send — same as a malformed string.
	grp, _ := json.Marshal(map[string]any{
		"to":       testJIDStr,
		"body":     "oi",
		"mentions": []string{"120363000000000000@g.us"},
	})
	if _, err := d.Handle(context.Background(), "send", grp); !errors.Is(err, app.ErrInvalidJID) {
		t.Fatalf("group mention: want ErrInvalidJID, got %v", err)
	}
	if len(adapter.Sent()) != 1 {
		t.Errorf("non-addressable mention must not send; got %d total", len(adapter.Sent()))
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
	for i := range 2 {
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

	// The denial must be audited as denied:rate (TEST-07): two ok sends
	// then the refusal — the audit log is the operator's only record of
	// why a send never left the box.
	entries := adapter.AuditEntries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 audit entries, got %d", len(entries))
	}
	if entries[2].Decision != "denied:rate" {
		t.Errorf("expected audit decision 'denied:rate', got %q", entries[2].Decision)
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

	// The denial must be audited as denied:warmup, distinct from
	// denied:rate (TEST-07): warmup refusals are expected during ramp-up
	// and the operator triages them differently from cap exhaustion.
	entries := adapter.AuditEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 audit entries, got %d", len(entries))
	}
	if entries[1].Decision != "denied:warmup" {
		t.Errorf("expected audit decision 'denied:warmup', got %q", entries[1].Decision)
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

// Spec 197: sendMedia with an inline `bytes` source sends correctly without
// a daemon-local path. The recorded domain message must carry the decoded
// bytes (Go json maps the base64 wire string ↔ []byte).
func TestSendMediaInlineBytes(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	want := []byte("\xff\xd8\xff\xe0inline-bytes")
	// map[string]any so `bytes` is a []byte → encoding/json emits base64.
	mediaParams, _ := json.Marshal(map[string]any{
		"to":      testJIDStr,
		"bytes":   want,
		"mime":    "image/jpeg",
		"caption": "inline",
	})
	result, err := d.Handle(context.Background(), "sendMedia", mediaParams)
	if err != nil {
		t.Fatalf("Handle(sendMedia bytes): %v", err)
	}
	var res struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("expected non-empty messageId")
	}

	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	mm, ok := sent[0].(domain.MediaMessage)
	if !ok {
		t.Fatalf("expected MediaMessage, got %T", sent[0])
	}
	if mm.Path != "" {
		t.Errorf("expected empty path on inline-bytes send, got %q", mm.Path)
	}
	if !bytes.Equal(mm.Bytes, want) {
		t.Errorf("recorded bytes mismatch:\n got %q\nwant %q", mm.Bytes, want)
	}
}

// sendMedia `filename` param reaches the domain message (and an absent mime
// still substitutes the adapter's detect sentinel). Regression surface for
// the user-reported ".bin" bug: remote sends carry sha256 + filename only.
func TestSendMediaFilenamePassthrough(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	mediaParams, _ := json.Marshal(map[string]any{
		"to":       testJIDStr,
		"bytes":    []byte("%PDF-1.7 tiny"),
		"filename": "hemograma.pdf",
	})
	if _, err := d.Handle(context.Background(), "sendMedia", mediaParams); err != nil {
		t.Fatalf("Handle(sendMedia filename): %v", err)
	}

	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	mm, ok := sent[0].(domain.MediaMessage)
	if !ok {
		t.Fatalf("expected MediaMessage, got %T", sent[0])
	}
	if mm.Filename != "hemograma.pdf" {
		t.Errorf("Filename = %q, want hemograma.pdf", mm.Filename)
	}
	if mm.Mime != "application/octet-stream" {
		t.Errorf("Mime = %q, want the octet-stream detect sentinel", mm.Mime)
	}
}

// Spec 197: malformed payload-source combinations on sendMedia surface
// ErrInvalidParams (-32602) before reaching the sender.
func TestSendMediaInvalidSourceCombos(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"path+bytes", map[string]any{"to": testJIDStr, "path": "/tmp/x.jpg", "bytes": []byte("h"), "mime": "image/jpeg"}},
		{"path+sha256", map[string]any{"to": testJIDStr, "path": "/tmp/x.jpg", "sha256": "deadbeef", "mime": "image/jpeg"}},
		{"bytes+sha256", map[string]any{"to": testJIDStr, "bytes": []byte("h"), "sha256": "deadbeef", "mime": "image/jpeg"}},
		{"no source", map[string]any{"to": testJIDStr, "mime": "image/jpeg"}},
		{"oversize bytes", map[string]any{"to": testJIDStr, "bytes": make([]byte, domain.MaxMediaBytes+1), "mime": "image/jpeg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.params)
			_, err := d.Handle(context.Background(), "sendMedia", raw)
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Errorf("expected ErrInvalidParams, got %v", err)
			}
		})
	}
	if len(adapter.Sent()) != 0 {
		t.Errorf("no malformed send should reach the sender; got %d", len(adapter.Sent()))
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

// notOnWhatsApp is an OnWhatsAppChecker that confirms every queried
// phone has no WhatsApp account.
type notOnWhatsApp struct{}

func (notOnWhatsApp) IsOnWhatsApp(context.Context, string) (domain.JID, bool, error) {
	return domain.JID{}, false, nil
}

// TEST-07: the dispatcher-level deliverability denial is surfaced as
// ErrNotOnWhatsApp, nothing is sent, and the refusal is audited as
// denied:not-on-whatsapp. The gate function itself is pinned by
// TestEnsureOnWhatsApp; this covers the Handle("send") wiring.
func TestSendDeniedNotOnWhatsApp(t *testing.T) {
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
		Quoted:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		OnWhatsApp:     notOnWhatsApp{},
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})
	_, err := d.Handle(context.Background(), "send", params)
	if !errors.Is(err, app.ErrNotOnWhatsApp) {
		t.Fatalf("expected ErrNotOnWhatsApp, got %v", err)
	}
	if len(adapter.Sent()) != 0 {
		t.Error("expected 0 sent messages")
	}
	entries := adapter.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Decision != "denied:not-on-whatsapp" {
		t.Errorf("expected audit decision 'denied:not-on-whatsapp', got %q", entries[0].Decision)
	}
}

// The voice-note hints are audio-only, and the refusal has to arrive as
// ErrInvalidParams (-32602) at the param boundary — before the rate limiter,
// before any upload. An omitted mime is included deliberately: it becomes the
// generic octet-stream sentinel above, which is not audio/*, so `--ptt` with
// no `--mime` is refused rather than silently sent as a file row.
func TestSendMediaAudioHintsRejected(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"ptt on image", map[string]any{"to": testJIDStr, "path": "/tmp/x.jpg", "mime": "image/jpeg", "ptt": true}},
		{"ptt with no mime", map[string]any{"to": testJIDStr, "path": "/tmp/x.ogg", "ptt": true}},
		{"seconds on document", map[string]any{"to": testJIDStr, "path": "/tmp/x.pdf", "mime": "application/pdf", "seconds": 7}},
		{"negative seconds", map[string]any{"to": testJIDStr, "path": "/tmp/x.ogg", "mime": "audio/ogg", "seconds": -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.params)
			_, err := d.Handle(context.Background(), "sendMedia", raw)
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Errorf("expected ErrInvalidParams, got %v", err)
			}
		})
	}
	if len(adapter.Sent()) != 0 {
		t.Errorf("no refused send should reach the sender; got %d", len(adapter.Sent()))
	}
}

// The accepted path: ptt + seconds on an audio mime reach the domain message
// intact. Nothing downstream can reconstruct them, so a param that is parsed
// but dropped here is indistinguishable from a plain audio attachment.
func TestSendMediaAudioHintsPassthrough(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	raw, _ := json.Marshal(map[string]any{
		"to":      testJIDStr,
		"bytes":   []byte("OggS fake"),
		"mime":    "audio/ogg; codecs=opus",
		"ptt":     true,
		"seconds": 7,
	})
	if _, err := d.Handle(context.Background(), "sendMedia", raw); err != nil {
		t.Fatalf("Handle(sendMedia ptt): %v", err)
	}

	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	mm, ok := sent[0].(domain.MediaMessage)
	if !ok {
		t.Fatalf("expected MediaMessage, got %T", sent[0])
	}
	if !mm.PTT {
		t.Error("PTT dropped — the recipient gets a file row, not a voice note")
	}
	if mm.Seconds != 7 {
		t.Errorf("Seconds = %d, want 7", mm.Seconds)
	}
}
