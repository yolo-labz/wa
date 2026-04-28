package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeBlocker is a minimal in-test app.Blocker used to drive dispatcher
// wiring for contact.block / contact.unblock / contact.blocklist and to
// simulate the server-side state consulted by ensureNotBlocked.
type fakeBlocker struct {
	mu      sync.Mutex
	blocked map[domain.JID]struct{}
}

func newFakeBlocker() *fakeBlocker {
	return &fakeBlocker{blocked: make(map[domain.JID]struct{})}
}

func (f *fakeBlocker) Block(_ context.Context, jid domain.JID) error {
	if jid.IsZero() {
		return domain.ErrInvalidJID
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked[jid] = struct{}{}
	return nil
}

func (f *fakeBlocker) Unblock(_ context.Context, jid domain.JID) error {
	if jid.IsZero() {
		return domain.ErrInvalidJID
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.blocked, jid)
	return nil
}

func (f *fakeBlocker) ListBlocked(_ context.Context) ([]domain.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.JID, 0, len(f.blocked))
	for j := range f.blocked {
		out = append(out, j)
	}
	return out, nil
}

func newBlockerDispatcher(t *testing.T) (*app.Dispatcher, *memory.Adapter, *fakeBlocker) {
	t.Helper()
	adapter := memory.New(nil)
	fb := newFakeBlocker()
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
		Blocker:        fb,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter, fb
}

// TestBlockedSendRefused32100 verifies that a send to a blocked JID is
// refused with domain.ErrBlocked (→ -32100 policy_refused at the socket
// boundary) and that no outbound Send call is made.
func TestBlockedSendRefused32100(t *testing.T) {
	d, adapter, fb := newBlockerDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	// Pre-block the target.
	if err := fb.Block(context.Background(), jid); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})
	_, err := d.Handle(context.Background(), "send", params)
	if !errors.Is(err, domain.ErrBlocked) {
		t.Fatalf("send to blocked JID err = %v, want ErrBlocked", err)
	}
	if n := len(adapter.Sent()); n != 0 {
		t.Errorf("want 0 sent messages, got %d", n)
	}
	// Audit entry recorded with denied:blocked decision.
	entries := adapter.AuditEntries()
	if len(entries) == 0 {
		t.Fatalf("expected denied:blocked audit entry, got 0")
	}
	last := entries[len(entries)-1]
	if last.Decision != "denied:blocked" {
		t.Errorf("last audit decision = %q, want denied:blocked", last.Decision)
	}
}

// TestContactBlockUnblockRoundTrip verifies that contact.block then
// contact.unblock leaves the server-side list empty, and contact.blocklist
// reflects each step.
func TestContactBlockUnblockRoundTrip(t *testing.T) {
	d, _, _ := newBlockerDispatcher(t)
	ctx := context.Background()

	blockParams, _ := json.Marshal(map[string]string{"jid": testJIDStr})
	if _, err := d.Handle(ctx, "contact.block", blockParams); err != nil {
		t.Fatalf("contact.block: %v", err)
	}

	listRaw, err := d.Handle(ctx, "contact.blocklist", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("contact.blocklist #1: %v", err)
	}
	var res struct {
		Blocked []string `json:"blocked"`
	}
	if err := json.Unmarshal(listRaw, &res); err != nil {
		t.Fatalf("unmarshal blocklist: %v", err)
	}
	if len(res.Blocked) != 1 || res.Blocked[0] != testJIDStr {
		t.Fatalf("after block want blocklist=[%s], got %+v", testJIDStr, res.Blocked)
	}

	if _, err := d.Handle(ctx, "contact.unblock", blockParams); err != nil {
		t.Fatalf("contact.unblock: %v", err)
	}

	listRaw2, err := d.Handle(ctx, "contact.blocklist", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("contact.blocklist #2: %v", err)
	}
	var res2 struct {
		Blocked []string `json:"blocked"`
	}
	if err := json.Unmarshal(listRaw2, &res2); err != nil {
		t.Fatalf("unmarshal blocklist #2: %v", err)
	}
	if len(res2.Blocked) != 0 {
		t.Fatalf("after unblock want empty blocklist, got %+v", res2.Blocked)
	}
}

// TestBlockerAbsentReturnsMethodNotFound verifies the dispatcher gracefully
// degrades when Blocker is nil — the handler returns ErrMethodNotFound.
func TestBlockerAbsentReturnsMethodNotFound(t *testing.T) {
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

	params, _ := json.Marshal(map[string]string{"jid": testJIDStr})
	_, err := d.Handle(context.Background(), "contact.block", params)
	if !errors.Is(err, app.ErrMethodNotFound) {
		t.Fatalf("contact.block without Blocker = %v, want ErrMethodNotFound", err)
	}
}
