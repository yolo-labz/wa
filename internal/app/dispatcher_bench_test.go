package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Benchmarks backing bench/README.md (roadmap 2.2). They measure the
// daemon-internal hot paths with the in-memory adapter — protocol
// round-trips excluded by design, so numbers are reproducible without
// a paired WhatsApp account.

// BenchmarkEventFanout measures bridge dispatch with one subscriber
// draining — the per-event cost every SSE/webhook/MCP consumer pays.
func BenchmarkEventFanout(b *testing.B) {
	stream := &fakeStream{}
	bridge := NewEventBridge(stream, slog.New(slog.DiscardHandler))
	go bridge.Run()
	ch, cancel := bridge.SubscribeStream(nil, 1024)
	// Drain so dispatch measures successful sends, not stream_drop.
	// Subscriber channels are never closed (cancel only deregisters),
	// so the drain goroutine needs an explicit quit signal.
	done := make(chan struct{})
	quit := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ch:
			case <-quit:
				return
			}
		}
	}()
	b.Cleanup(func() {
		cancel()
		bridge.Close()
		close(quit)
		<-done
	})
	jid := domain.MustJID("12025550100@s.whatsapp.net")
	evt := domain.MessageEvent{
		ID: "bench", TS: time.Now(), From: jid,
		Message: domain.TextMessage{Recipient: jid, Body: "benchmark message body of realistic length for a chat app"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		bridge.dispatch(translateDomainEvent(evt), true)
	}
}

// BenchmarkChannelWrap measures the FR-005a envelope cost — applied to
// every inbound message body crossing the subscriber boundary.
func BenchmarkChannelWrap(b *testing.B) {
	jid := domain.MustJID("12025550100@s.whatsapp.net")
	fields := InboundFields{Body: "benchmark message body of realistic length for a chat app", PushName: "Bench Mark"}
	b.ReportAllocs()
	for b.Loop() {
		_ = ChannelWrapFields(fields, jid, jid, 1781000000)
	}
}

// BenchmarkDraftCreate measures the MCP draft-gate primitive end to
// end (parse → validate → payload marshal → store put → audit).
func BenchmarkDraftCreate(b *testing.B) {
	d := &Dispatcher{
		profile: "bench",
		drafts:  newBenchDraftStore(),
		log:     slog.New(slog.DiscardHandler),
	}
	ctx := context.Background()
	raw := json.RawMessage(`{"to":"5511999999999","body":"bench","origin":"bench"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := d.handleDraftCreate(ctx, raw); err != nil {
			b.Fatal(err)
		}
	}
}

// benchDraftStore is a no-op DraftStore so the benchmark measures the
// dispatcher path, not SQLite.
type benchDraftStore struct{}

func newBenchDraftStore() *benchDraftStore { return &benchDraftStore{} }

func (s *benchDraftStore) Put(context.Context, domain.Draft) error { return nil }
func (s *benchDraftStore) Get(context.Context, string, string) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (s *benchDraftStore) List(context.Context, string, domain.DraftState, int) ([]domain.Draft, error) {
	return nil, nil
}

func (s *benchDraftStore) Approve(context.Context, string, string, time.Time, domain.DraftDecider) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (s *benchDraftStore) Reject(context.Context, string, string, time.Time, domain.DraftDecider, string) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (s *benchDraftStore) ExpireDue(context.Context, string, time.Time) (int, error) { return 0, nil }
