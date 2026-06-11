package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// presenceRecorder is a minimal PresenceSender double recording the state
// sequence reaching the port.
type presenceRecorder struct {
	mu     sync.Mutex
	states []string
	err    error
}

func (r *presenceRecorder) SendComposing(_ context.Context, _ domain.JID, state string, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, state)
	return r.err
}

func (r *presenceRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.states...)
}

func newHumanizeDispatcher(rec *presenceRecorder, delay time.Duration) *Dispatcher {
	d := &Dispatcher{
		log:             slog.New(slog.DiscardHandler),
		humanizeDelayFn: func(int) time.Duration { return delay },
	}
	if rec != nil {
		d.presenceSender = rec
	}
	return d
}

var humanizeTestJID = domain.MustJID("5511999999999@s.whatsapp.net")

// TestHumanizeBeforeSendOrder verifies the canonical flow: composing →
// delay → paused, returning nil.
func TestHumanizeBeforeSendOrder(t *testing.T) {
	t.Parallel()
	rec := &presenceRecorder{}
	d := newHumanizeDispatcher(rec, time.Millisecond)

	start := time.Now()
	if err := d.humanizeBeforeSend(context.Background(), humanizeTestJID, 5); err != nil {
		t.Fatalf("humanizeBeforeSend: %v", err)
	}
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("elapsed %v, want >= the injected 1ms delay", elapsed)
	}
	got := rec.recorded()
	if len(got) != 2 || got[0] != "composing" || got[1] != "paused" {
		t.Errorf("presence sequence = %v, want [composing paused]", got)
	}
}

// TestHumanizeCtxCanceledDuringDelay verifies a context deadline during
// the delay aborts the send with ctx.Err() and never emits paused.
func TestHumanizeCtxCanceledDuringDelay(t *testing.T) {
	t.Parallel()
	rec := &presenceRecorder{}
	d := newHumanizeDispatcher(rec, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := d.humanizeBeforeSend(ctx, humanizeTestJID, 5)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	got := rec.recorded()
	if len(got) != 1 || got[0] != "composing" {
		t.Errorf("presence sequence = %v, want [composing] only (no paused after abort)", got)
	}
}

// TestHumanizeNoPresenceDelayOnly verifies the flow degrades to delay-only
// when no PresenceSender is wired.
func TestHumanizeNoPresenceDelayOnly(t *testing.T) {
	t.Parallel()
	d := newHumanizeDispatcher(nil, time.Millisecond)
	if err := d.humanizeBeforeSend(context.Background(), humanizeTestJID, 5); err != nil {
		t.Fatalf("humanizeBeforeSend without presence: %v", err)
	}
}

// TestHumanizePresenceErrFailOpen verifies a failing presence frame never
// blocks the send: both frames are attempted, the flow still returns nil.
func TestHumanizePresenceErrFailOpen(t *testing.T) {
	t.Parallel()
	rec := &presenceRecorder{err: errors.New("wire down")}
	d := newHumanizeDispatcher(rec, time.Millisecond)
	if err := d.humanizeBeforeSend(context.Background(), humanizeTestJID, 5); err != nil {
		t.Fatalf("humanizeBeforeSend with failing presence: %v", err)
	}
	if got := rec.recorded(); len(got) != 2 {
		t.Errorf("presence attempts = %v, want both frames attempted", got)
	}
}

// TestDefaultHumanizeDelayBounds verifies the delay model: jitter stays in
// [0.75, 1.25]× of min(base + perChar×len, cap), and the floor clears the
// 1 s presence budget so the trailing paused frame is never budget-dropped.
func TestDefaultHumanizeDelayBounds(t *testing.T) {
	t.Parallel()
	for _, bodyLen := range []int{0, 40, 100000} {
		expected := humanizeBaseDelay + time.Duration(bodyLen)*humanizePerChar
		if expected > humanizeMaxDelay {
			expected = humanizeMaxDelay
		}
		lo := time.Duration(float64(expected) * 0.75)
		hi := time.Duration(float64(expected) * 1.25)
		for range 50 {
			d := defaultHumanizeDelay(bodyLen)
			if d < lo || d > hi {
				t.Fatalf("len=%d: delay %v outside [%v, %v]", bodyLen, d, lo, hi)
			}
			if d <= time.Second {
				t.Fatalf("len=%d: delay %v under the 1s presence budget — paused frame would drop", bodyLen, d)
			}
		}
	}
}
