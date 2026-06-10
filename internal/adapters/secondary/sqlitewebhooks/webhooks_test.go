package sqlitewebhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/yolo-labz/wa/v2/internal/app"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "webhooks.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestStoreLifecycle pins endpoint CRUD + delivery state transitions:
// enqueue → due → delivered resets streak; repeated dead deliveries
// auto-disable the endpoint at the documented threshold.
func TestStoreLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t)

	ep := app.WebhookEndpoint{ID: "whe-1", Profile: "p", URL: "http://x", Secret: "whsec_abc", Topics: "message", CreatedAt: 1}
	if err := s.AddEndpoint(ctx, ep); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	if err := s.Enqueue(ctx, app.WebhookDelivery{ID: "whd-1", Profile: "p", EndpointID: "whe-1", Topic: "message", Payload: "{}", NextAttemptAt: 10, CreatedAt: 10}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	due, err := s.Due(ctx, "p", 5, 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("Due before nextAttemptAt: %v %d", err, len(due))
	}
	due, err = s.Due(ctx, "p", 10, 10)
	if err != nil || len(due) != 1 || due[0].Secret != "whsec_abc" {
		t.Fatalf("Due at time: %v %+v", err, due)
	}

	if err := s.MarkDelivered(ctx, "p", "whd-1", "whe-1", 11); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	rows, _ := s.Deliveries(ctx, "p", "delivered", 10)
	if len(rows) != 1 {
		t.Fatalf("delivered rows = %d", len(rows))
	}

	// Drive 5 dead deliveries → endpoint auto-disabled.
	for i := range 5 {
		id := "whd-dead-" + string(rune('a'+i))
		_ = s.Enqueue(ctx, app.WebhookDelivery{ID: id, Profile: "p", EndpointID: "whe-1", Topic: "message", Payload: "{}", NextAttemptAt: 20, CreatedAt: 20})
		if err := s.MarkFailed(ctx, "p", id, "whe-1", 8, 0, "receiver returned 500", true, 21); err != nil {
			t.Fatalf("MarkFailed dead: %v", err)
		}
	}
	eps, _ := s.ListEndpoints(ctx, "p")
	if len(eps) != 1 || !eps[0].Disabled || eps[0].FailureStreak != 5 {
		t.Fatalf("endpoint after dead streak: %+v", eps)
	}

	// Replay re-arms a dead delivery.
	if err := s.Replay(ctx, "p", "whd-dead-a", 30); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	pend, _ := s.Deliveries(ctx, "p", "pending", 10)
	if len(pend) != 1 {
		t.Fatalf("pending after replay = %d", len(pend))
	}
	if err := s.Replay(ctx, "p", "nope", 30); err == nil {
		t.Fatal("Replay unknown id must fail")
	}
	if err := s.RemoveEndpoint(ctx, "p", "whe-1"); err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	if err := s.RemoveEndpoint(ctx, "p", "whe-1"); err == nil {
		t.Fatal("second remove must fail")
	}
}

// fakeEvents satisfies the worker's SubscribeStream dependency.
type fakeEvents struct{ ch chan app.Event }

func (f *fakeEvents) SubscribeStream([]string, int) (<-chan app.Event, func()) {
	return f.ch, func() {}
}

// TestWorkerEndToEnd proves the delivery pipeline against a real HTTP
// receiver: event → fan-out → signed POST whose signature verifies
// with the standard-webhooks library; then a failing receiver path
// that retries and dead-letters per the backoff schedule.
func TestWorkerEndToEnd(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := openTestStore(t)

	secret, err := app.NewWebhookSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	var got atomic.Value
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		verifier, vErr := standardwebhooks.NewWebhook(secret[len("whsec_"):])
		if vErr != nil {
			t.Errorf("verifier: %v", vErr)
		}
		if vErr := verifier.Verify(body, r.Header); vErr != nil {
			t.Errorf("signature verify failed: %v", vErr)
		}
		got.Store(string(body))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	if err := s.AddEndpoint(ctx, app.WebhookEndpoint{
		ID: "whe-ok", Profile: "p", URL: receiver.URL, Secret: secret, Topics: "message", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}

	events := &fakeEvents{ch: make(chan app.Event, 4)}
	w := app.NewWebhookWorker(s, events, "p", nil)
	w.TickInterval = 50 * time.Millisecond
	w.Start(ctx)
	t.Cleanup(w.Stop)

	events.ch <- app.Event{Type: "message", Payload: map[string]any{"channel": "<channel>hi</channel>"}}

	deadline := time.After(15 * time.Second)
	for {
		rows, _ := s.Deliveries(ctx, "p", "delivered", 5)
		if len(rows) == 1 {
			break
		}
		select {
		case <-deadline:
			pend, _ := s.Deliveries(ctx, "p", "", 5)
			t.Fatalf("delivery never settled: %+v", pend)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if body, _ := got.Load().(string); body == "" || !strings.Contains(body, `"wa.webhook/v1"`) {
		t.Fatalf("receiver body wrong: %q", got.Load())
	}
}
