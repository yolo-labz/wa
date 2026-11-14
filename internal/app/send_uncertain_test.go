package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// uncertainSender is the adapter's answer to an ack that never came.
type uncertainSender struct{ *memory.Adapter }

func (uncertainSender) Send(context.Context, domain.Message) (domain.MessageID, error) {
	return "", domain.NewSendUncertain("WAID-U", errors.New("ack timeout"))
}

// A send that may have been delivered keeps its type through the
// dispatcher (the job and the wire map it) and is audited as such, not as
// a plain "error" (F4).
func TestSend_UncertainSurvivesTheDispatcherAndIsAudited(t *testing.T) {
	adapter := memory.New(nil)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: uncertainSender{adapter}, Events: adapter, Contacts: adapter, Groups: adapter,
		Session: adapter, Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter,
		Profile: "default", SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	_, err := d.Handle(context.Background(), "send", json.RawMessage(`{"to":"`+testJIDStr+`","body":"hi"}`))
	var u *domain.SendUncertainError
	if !errors.Is(err, domain.ErrSendUncertain) || !errors.As(err, &u) || u.MessageID != "WAID-U" {
		t.Fatalf("send err = %v, want ErrSendUncertain carrying WAID-U", err)
	}
	var last domain.AuditEvent
	for _, e := range adapter.AuditEntries() {
		last = e
	}
	if last.Detail != "send_uncertain" {
		t.Errorf("audit detail %q, want send_uncertain", last.Detail)
	}
}

type countingUncertainSender struct {
	*memory.Adapter
	calls atomic.Int32
}

func (s *countingUncertainSender) Send(context.Context, domain.Message) (domain.MessageID, error) {
	s.calls.Add(1)
	return "", domain.NewSendUncertain("WAID-K", errors.New("ack timeout"))
}

// A keyed send that ends send_uncertain is recorded under its key: a retry
// with the same key is told the same thing (and the id) without a second wire
// send; a different key is a new send.
func TestSend_KeyedUncertainIsRecordedAndNotResent(t *testing.T) {
	adapter := memory.New(nil)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
	snd := &countingUncertainSender{Adapter: adapter}
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: snd, Events: adapter, Contacts: adapter, Groups: adapter, Session: adapter,
		Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter,
		Idempotency: memory.NewIdempotencyStore(), Profile: "default",
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	send := func(key string) error {
		_, err := d.Handle(context.Background(), "send", json.RawMessage(`{"to":"`+testJIDStr+`","body":"hi","idempotencyKey":"`+key+`"}`))
		return err
	}
	for i := range 2 {
		err := send("k-1")
		var u *domain.SendUncertainError
		if !errors.As(err, &u) || u.MessageID != "WAID-K" {
			t.Fatalf("call %d: %v, want send_uncertain carrying WAID-K", i, err)
		}
	}
	if n := snd.calls.Load(); n != 1 {
		t.Fatalf("%d wire sends for one key, want 1", n)
	}
	_ = send("k-2")
	if n := snd.calls.Load(); n != 2 {
		t.Errorf("a different key must send: %d calls", n)
	}
}
