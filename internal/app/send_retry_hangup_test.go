package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// slowSender is the wire: every Send takes 20 s and is counted.
type slowSender struct {
	*memory.Adapter
	wire atomic.Int32
}

func (s *slowSender) Send(ctx context.Context, m domain.Message) (domain.MessageID, error) {
	s.wire.Add(1)
	<-time.After(20 * time.Second)
	return s.Adapter.Send(context.WithoutCancel(ctx), m)
}

// TestKeyedSendRetryAfterHangUpSendsOnce (p8 M1): a send whose client hangs
// up at 12 s keeps running; the same caller retrying with the same
// idempotencyKey at 15 s must join it and get the original result, not
// start a second wire send. The claim is taken at call start.
func TestKeyedSendRetryAfterHangUpSendsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		adapter := memory.New(nil)
		adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
		snd := &slowSender{Adapter: adapter}
		d := app.NewDispatcher(app.DispatcherConfig{
			Sender: snd, Events: adapter, Contacts: adapter, Groups: adapter, Session: adapter,
			Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter,
			Idempotency: memory.NewIdempotencyStore(), Profile: "default",
			SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		})
		e := app.NewExecutor(app.DefaultExecLimits, app.ExecAuth{})
		defer func() { e.Close(); _ = d.Close(); synctest.Wait() }()
		raw := json.RawMessage(`{"to":"` + testJIDStr + `","body":"hi","idempotencyKey":"k-1"}`)
		call := func(ctx context.Context) (json.RawMessage, error) {
			return e.Run(ctx, "op", "send", false, func(c context.Context) (json.RawMessage, error) {
				return d.Handle(c, "send", raw)
			})
		}

		hung, hangUp := context.WithCancel(t.Context())
		go func() { <-time.After(12 * time.Second); hangUp() }()
		if _, err := call(hung); !errors.Is(err, context.Canceled) {
			t.Fatalf("first call: %v, want the hang-up", err)
		}
		<-time.After(3 * time.Second) // t = 15 s: the first send is still on the wire
		out, err := call(t.Context())
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		var res struct {
			MessageID string `json:"messageId"`
		}
		if err := json.Unmarshal(out, &res); err != nil || res.MessageID == "" {
			t.Fatalf("retry result %s (%v), want the original send's id", out, err)
		}
		if n := snd.wire.Load(); n != 1 {
			t.Fatalf("%d wire sends for one idempotency key, want 1", n)
		}
	})
}

// L2: same-key retries must not exhaust the owner's in-flight cap. One retry
// waits for the first call; the other 19 are answered -32024 at once, so an
// unrelated send from the same owner still runs while they "wait".
func TestKeyedRetriesDoNotExhaustTheOwnersCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		adapter := memory.New(nil)
		adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
		snd := &slowSender{Adapter: adapter}
		d := app.NewDispatcher(app.DispatcherConfig{
			Sender: snd, Events: adapter, Contacts: adapter, Groups: adapter, Session: adapter,
			Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter,
			Idempotency: memory.NewIdempotencyStore(), Profile: "default",
			SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		})
		e := app.NewExecutor(app.DefaultExecLimits, app.ExecAuth{})
		defer func() { e.Close(); _ = d.Close(); synctest.Wait() }()
		send := func(key string) error {
			raw := json.RawMessage(`{"to":"` + testJIDStr + `","body":"hi","idempotencyKey":"` + key + `"}`)
			_, err := e.Run(t.Context(), "op", "send", false, func(c context.Context) (json.RawMessage, error) {
				return d.Handle(c, "send", raw)
			})
			return err
		}
		const retries = 20
		errs := make(chan error, retries+2)
		go func() { errs <- send("k-1") }()
		synctest.Wait()     // the first call is on the wire
		for range retries { // arriving one after another, as retries do
			go func() { errs <- send("k-1") }()
			synctest.Wait()
		}
		turnedAway := 0
		for range retries - 1 {
			if err := <-errs; !errors.Is(err, domain.ErrIdempotencyInFlight) {
				t.Fatalf("a surplus retry got %v, want ErrIdempotencyInFlight at once", err)
			}
			turnedAway++
		}
		if err := send("k-2"); err != nil { // unrelated, same owner, while the retries "wait"
			t.Fatalf("unrelated send while same-key retries wait: %v", err)
		}
		for range 2 { // the first call and the one parked retry
			if err := <-errs; err != nil {
				t.Errorf("first/parked call: %v", err)
			}
		}
		if n := snd.wire.Load(); n != 2 {
			t.Errorf("%d wire sends, want 2 (k-1 once, k-2 once); turned away %d", n, turnedAway)
		}
	})
}
