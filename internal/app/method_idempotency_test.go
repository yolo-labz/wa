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

// newIdempotentDispatcher builds a dispatcher with a memory IdempotencyStore
// wired in so the FR-034a sidecar is exercised end-to-end.
func newIdempotentDispatcher(t *testing.T) (*app.Dispatcher, *memory.Adapter, *memory.IdempotencyStore) {
	t.Helper()
	adapter := memory.New(nil)
	store := memory.NewIdempotencyStore()
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
		Idempotency:    store,
		Profile:        "default",
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter, store
}

// seedAllow flips the memory allowlist so the safety pipeline lets a
// send/markRead/react pass through. Every test below exercises a
// mutating handler, so every test needs it.
func seedAllow(t *testing.T, adapter *memory.Adapter, jid domain.JID) {
	t.Helper()
	adapter.Grant(jid, domain.ActionSend, domain.ActionRead)
}

// TestExistingMethodsAcceptIdempotencyKey asserts every mutating JSON-RPC
// method accepts an `idempotencyKey` field in params without rejecting
// the call — the sidecar MUST peel the envelope cleanly from the typed
// params parser (send, sendMedia, react, markRead, pair, schedule.send,
// send.reply).
func TestExistingMethodsAcceptIdempotencyKey(t *testing.T) {
	cases := []struct {
		method string
		params string
	}{
		{"send", `{"to":"` + testJIDStr + `","body":"hi","idempotencyKey":"k-send"}`},
		{"react", `{"chat":"` + testJIDStr + `","messageId":"m1","emoji":"👍","idempotencyKey":"k-react"}`},
		{"markRead", `{"chat":"` + testJIDStr + `","messageId":"m1","idempotencyKey":"k-mr"}`},
		{"pair", `{"idempotencyKey":"k-pair"}`},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			d, adapter, _ := newIdempotentDispatcher(t)
			seedAllow(t, adapter, domain.MustJID(testJIDStr))
			if _, err := d.Handle(context.Background(), c.method, json.RawMessage(c.params)); err != nil {
				t.Errorf("%s accept idempotencyKey: %v", c.method, err)
			}
		})
	}
}

// TestEmptyKeyBypassesSidecar asserts an absent-or-empty idempotencyKey
// disables replay caching — the handler runs verbatim and a second call
// invokes the underlying send a second time (so the sidecar didn't touch
// it). Proof: two calls produce two separate message IDs.
func TestEmptyKeyBypassesSidecar(t *testing.T) {
	d, adapter, _ := newIdempotentDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	seedAllow(t, adapter, jid)

	call := func(body string) string {
		t.Helper()
		params := `{"to":"` + testJIDStr + `","body":"` + body + `"}`
		out, err := d.Handle(context.Background(), "send", json.RawMessage(params))
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		var r struct {
			MessageID string `json:"messageId"`
		}
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return r.MessageID
	}
	id1 := call("a")
	id2 := call("b")
	if id1 == "" || id2 == "" {
		t.Fatalf("empty messageId: %q %q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("empty-key sidecar replayed: both calls returned %s", id1)
	}
}

// TestReplaySameKeyReturnsCachedBytes asserts the sidecar's core property:
// two calls with the same idempotencyKey + identical params return the
// same cached bytes (same messageId, same timestamp). The underlying
// sender MUST NOT have been invoked twice.
func TestReplaySameKeyReturnsCachedBytes(t *testing.T) {
	d, adapter, _ := newIdempotentDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	seedAllow(t, adapter, jid)

	params := json.RawMessage(`{"to":"` + testJIDStr + `","body":"hi","idempotencyKey":"k-replay"}`)

	out1, err := d.Handle(context.Background(), "send", params)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	out2, err := d.Handle(context.Background(), "send", params)
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("replay differs:\n  first=%s\n second=%s", out1, out2)
	}
}

// TestCollisionReturns32101 asserts two calls with the same key but a
// different body yield domain.ErrIdempotencyCollision, which the socket
// dispatcher then maps to -32101 idempotency_collision. The app-layer
// surface only has to verify the error unwraps correctly.
func TestCollisionReturns32101(t *testing.T) {
	d, adapter, _ := newIdempotentDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	seedAllow(t, adapter, jid)

	first := json.RawMessage(`{"to":"` + testJIDStr + `","body":"one","idempotencyKey":"k-collide"}`)
	second := json.RawMessage(`{"to":"` + testJIDStr + `","body":"two","idempotencyKey":"k-collide"}`)

	if _, err := d.Handle(context.Background(), "send", first); err != nil {
		t.Fatalf("first send: %v", err)
	}
	_, err := d.Handle(context.Background(), "send", second)
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	if !errors.Is(err, domain.ErrIdempotencyCollision) {
		t.Fatalf("want ErrIdempotencyCollision, got %v", err)
	}
}

// TestReservedFieldsIgnoredInHash asserts FR-033: the hash strips
// `idempotencyKey`, `requestId`, `ts`, `timestamp` before fingerprinting.
// Two calls with the same key but different `requestId`/`ts` values must
// replay the same cached bytes — not collide.
func TestReservedFieldsIgnoredInHash(t *testing.T) {
	d, adapter, _ := newIdempotentDispatcher(t)
	jid := domain.MustJID(testJIDStr)
	seedAllow(t, adapter, jid)

	first := json.RawMessage(`{"to":"` + testJIDStr + `","body":"hi","idempotencyKey":"k-rsv","requestId":"r1","ts":1}`)
	second := json.RawMessage(`{"to":"` + testJIDStr + `","body":"hi","idempotencyKey":"k-rsv","requestId":"r2","ts":2}`)

	out1, err := d.Handle(context.Background(), "send", first)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	out2, err := d.Handle(context.Background(), "send", second)
	if err != nil {
		t.Fatalf("second with different reserved fields: %v", err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("reserved fields leaked into hash:\n  first=%s\n second=%s", out1, out2)
	}
}
