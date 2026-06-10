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

// pumpStream is a channel-backed EventStream. Unlike the memory
// adapter — whose Next blocks forever once its seed queue drains —
// events pushed here after the bridge starts are still delivered,
// which the wait method needs (the waiter registers only when the
// RPC arrives, long after the bridge began consuming the stream).
type pumpStream struct{ events chan domain.Event }

func newPumpStream() *pumpStream {
	return &pumpStream{events: make(chan domain.Event)}
}

func (p *pumpStream) Next(ctx context.Context) (domain.Event, error) {
	select {
	case e := <-p.events:
		return e, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pumpStream) Ack(domain.EventID) error { return nil }
func (p *pumpStream) ResumeFrom(uint64) error  { return nil }

func newWaitDispatcher(t *testing.T) (*app.Dispatcher, *pumpStream) {
	t.Helper()
	adapter := memory.New(nil)
	pump := newPumpStream()
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender:    adapter,
		Events:    pump,
		Contacts:  adapter,
		Groups:    adapter,
		Session:   adapter,
		Allowlist: adapter,
		Audit:     adapter,
		History:   adapter,
		Pairer:    adapter,
		Quoted:    adapter,
	})
	t.Cleanup(func() { _ = d.Close() })
	return d, pump
}

type waitOutcome struct {
	raw json.RawMessage
	err error
}

// startWait invokes the wait method on its own goroutine. The waiter
// registers asynchronously inside Handle, so callers must keep pumping
// events until the outcome channel fires (beats the registration race —
// events dispatched before registration are dropped by design).
func startWait(d *app.Dispatcher, params json.RawMessage) <-chan waitOutcome {
	resCh := make(chan waitOutcome, 1)
	go func() {
		raw, err := d.Handle(context.Background(), "wait", params)
		resCh <- waitOutcome{raw: raw, err: err}
	}()
	return resCh
}

// TestWait_ReceivesEvent pins FR-029/FR-031: nil params are valid
// (default timeout, match-all filter) and the first delivered event is
// returned as {Type, Payload}.
func TestWait_ReceivesEvent(t *testing.T) {
	d, pump := newWaitDispatcher(t)
	resCh := startWait(d, nil)

	evt := domain.ReceiptEvent{
		ID:        "e1",
		TS:        time.Unix(1_781_000_000, 0),
		Chat:      domain.MustJID(testJIDStr),
		MessageID: "MSG-1",
		Status:    domain.ReceiptRead,
	}
	var got waitOutcome
pumping:
	for {
		select {
		case got = <-resCh:
			break pumping
		case pump.events <- evt:
			// Keep pushing until the waiter has registered and consumed one.
		}
	}
	if got.err != nil {
		t.Fatalf("Handle(wait): %v", got.err)
	}

	var wire struct {
		Type    string         `json:"Type"`
		Payload map[string]any `json:"Payload"`
	}
	if err := json.Unmarshal(got.raw, &wire); err != nil {
		t.Fatalf("unmarshal wait result %q: %v", string(got.raw), err)
	}
	if wire.Type != "receipt" {
		t.Errorf("Type = %q, want receipt", wire.Type)
	}
	if id, _ := wire.Payload["MessageID"].(string); id != "MSG-1" {
		t.Errorf("payload MessageID = %v, want MSG-1", wire.Payload["MessageID"])
	}
}

// TestWait_Filter pins FR-030: a ["receipt"] filter skips status events
// even when they arrive first.
func TestWait_Filter(t *testing.T) {
	d, pump := newWaitDispatcher(t)
	params, _ := json.Marshal(map[string]any{"events": []string{"receipt"}, "timeoutMs": 5000})
	resCh := startWait(d, params)

	status := domain.ConnectionEvent{ID: "e0", TS: time.Unix(1_781_000_000, 0), State: domain.ConnConnected}
	receipt := domain.ReceiptEvent{
		ID:        "e1",
		TS:        time.Unix(1_781_000_001, 0),
		Chat:      domain.MustJID(testJIDStr),
		MessageID: "MSG-2",
		Status:    domain.ReceiptDelivered,
	}
	var got waitOutcome
pumping:
	for {
		select {
		case got = <-resCh:
			break pumping
		case pump.events <- status:
			// Status first each round: a broken filter would return it.
			pump.events <- receipt
		}
	}
	if got.err != nil {
		t.Fatalf("Handle(wait): %v", got.err)
	}

	var wire struct {
		Type string `json:"Type"`
	}
	if err := json.Unmarshal(got.raw, &wire); err != nil {
		t.Fatalf("unmarshal wait result: %v", err)
	}
	if wire.Type != "receipt" {
		t.Errorf("Type = %q, want receipt (status must be filtered out)", wire.Type)
	}
}

// TestWait_Timeout pins FR-031: no matching event within timeoutMs maps
// to ErrWaitTimeout.
func TestWait_Timeout(t *testing.T) {
	d, _ := newWaitDispatcher(t)

	params, _ := json.Marshal(map[string]any{"timeoutMs": 50})
	_, err := d.Handle(context.Background(), "wait", params)
	if !errors.Is(err, app.ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout", err)
	}
}

// TestWait_BadParams pins that malformed JSON is refused before a
// waiter is registered.
func TestWait_BadParams(t *testing.T) {
	d, _ := newWaitDispatcher(t)

	_, err := d.Handle(context.Background(), "wait", json.RawMessage(`{bad`))
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
}
