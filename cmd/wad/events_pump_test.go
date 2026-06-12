package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// fakePumpSource is a hand-rolled eventsPumpSource: tests push into the
// channel; cancel just records it ran.
type fakePumpSource struct {
	ch        chan app.Event
	cancelled bool
}

func (f *fakePumpSource) SubscribeStream(_ []string, _ int) (<-chan app.Event, func()) {
	return f.ch, func() { f.cancelled = true }
}

// fakeEventBuffer records Append calls; failKinds simulates per-kind
// append errors.
type fakeEventBuffer struct {
	mu        sync.Mutex
	recs      []app.EventRecord
	failKinds map[string]bool
}

func (f *fakeEventBuffer) Append(_ context.Context, rec app.EventRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failKinds[rec.Kind] {
		return errors.New("boom")
	}
	f.recs = append(f.recs, rec)
	return nil
}

func (f *fakeEventBuffer) Range(context.Context, int64, int) ([]app.EventRecord, error) {
	return nil, nil
}

func (f *fakeEventBuffer) Stats(context.Context) (app.BufferStats, error) {
	return app.BufferStats{}, nil
}
func (f *fakeEventBuffer) TrimTo(context.Context, int64) (int, error) { return 0, nil }

func (f *fakeEventBuffer) kinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.recs))
	for _, r := range f.recs {
		out = append(out, r.Kind)
	}
	return out
}

// TestEventsPump_AppendsTranslatedEvents pins the ARCH-01 wire: every
// fan-out event lands in the durable ring as an EventRecord whose
// TrustedJSON is the marshalled subscriber payload.
func TestEventsPump_AppendsTranslatedEvents(t *testing.T) {
	src := &fakePumpSource{ch: make(chan app.Event, 4)}
	buf := &fakeEventBuffer{}
	stop := startEventsPump(context.Background(), src, buf, quietLogger())

	src.ch <- app.Event{Type: "message", Payload: map[string]any{"id": "M1"}}
	src.ch <- app.Event{Type: "receipt", Payload: map[string]any{"id": "R1"}}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(buf.kinds()) == 2 {
			break
		}
		<-time.After(10 * time.Millisecond)
	}
	stop()
	if !src.cancelled {
		t.Error("stop() must cancel the fan-out subscription")
	}

	got := buf.kinds()
	if len(got) != 2 || got[0] != "message" || got[1] != "receipt" {
		t.Fatalf("appended kinds = %v, want [message receipt]", got)
	}
	buf.mu.Lock()
	defer buf.mu.Unlock()
	if !strings.Contains(string(buf.recs[0].TrustedJSON), `"id":"M1"`) {
		t.Errorf("TrustedJSON = %s, want to contain M1", buf.recs[0].TrustedJSON)
	}
}

// TestEventsPump_AppendErrorContinues pins the best-effort posture: one
// failing append never stalls the pump — later events still land.
func TestEventsPump_AppendErrorContinues(t *testing.T) {
	src := &fakePumpSource{ch: make(chan app.Event, 4)}
	buf := &fakeEventBuffer{failKinds: map[string]bool{"receipt": true}}
	stop := startEventsPump(context.Background(), src, buf, quietLogger())
	defer stop()

	src.ch <- app.Event{Type: "receipt", Payload: "x"} // fails
	src.ch <- app.Event{Type: "message", Payload: "y"} // must still land

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(buf.kinds()) == 1 {
			return
		}
		<-time.After(10 * time.Millisecond)
	}
	t.Fatalf("appended kinds = %v, want [message] after receipt append failure", buf.kinds())
}

// TestEventsPump_NilStoreNoop pins the SF-03 degraded posture: a nil
// store returns a no-op stop and subscribes nothing.
func TestEventsPump_NilStoreNoop(t *testing.T) {
	src := &fakePumpSource{ch: make(chan app.Event)}
	stop := startEventsPump(context.Background(), src, nil, quietLogger())
	stop() // must not hang or panic
	if src.cancelled {
		t.Error("nil store must not subscribe at all")
	}
}
