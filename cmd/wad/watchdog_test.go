package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestParseSoftStaleThresholdSec covers every branch of spec 110g
// FR-007: unset/empty/zero → disabled, garbage → disabled+warn, below
// floor → clamp UP, above ceiling → clamp DOWN, in-band → pass through.
// One table, one log capture — keeps the env-parse contract in a single
// readable place.
func TestParseSoftStaleThresholdSec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in        string
		want      int
		wantWarn  bool
		wantSubst string // optional substring that the warning must mention
	}{
		{in: "", want: 0, wantWarn: false},
		{in: "0", want: 0, wantWarn: false},
		{in: "-7", want: 0, wantWarn: false}, // negative collapses silently
		{in: "abc", want: 0, wantWarn: true, wantSubst: "not an integer"},
		{in: "5", want: 30, wantWarn: true, wantSubst: "clamped UP"},
		{in: "29", want: 30, wantWarn: true, wantSubst: "clamped UP"},
		{in: "30", want: 30, wantWarn: false},
		{in: "300", want: 300, wantWarn: false},
		{in: "3600", want: 3600, wantWarn: false},
		{in: "9999", want: 3600, wantWarn: true, wantSubst: "clamped DOWN"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			var buf threadSafeBuffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			got := ParseSoftStaleThresholdSec(tc.in, log)
			if got != tc.want {
				t.Errorf("ParseSoftStaleThresholdSec(%q) = %d, want %d", tc.in, got, tc.want)
			}
			out := buf.String()
			hasWarn := strings.Contains(out, "level=WARN")
			if hasWarn != tc.wantWarn {
				t.Errorf("warn emitted = %v, want %v (log=%q)", hasWarn, tc.wantWarn, out)
			}
			if tc.wantSubst != "" && !strings.Contains(out, tc.wantSubst) {
				t.Errorf("warning missing %q in %q", tc.wantSubst, out)
			}
		})
	}
}

// TestSoftStaleTickFor asserts the tick period stays inside the [1s,60s]
// envelope and tracks threshold/3 in between. The tick cap protects the
// goroutine from busy-spinning on very long thresholds; the floor keeps
// detection latency reasonable on a 30s threshold.
func TestSoftStaleTickFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		threshold int
		want      time.Duration
	}{
		{1, 1 * time.Second},
		{30, 10 * time.Second}, // 30/3
		{60, 20 * time.Second},
		{180, 60 * time.Second}, // 180/3 = 60s exactly
		{300, 60 * time.Second}, // 300/3 = 100 → clamped to 60
		{3600, 60 * time.Second},
	}
	for _, tc := range cases {
		if got := softStaleTickFor(tc.threshold); got != tc.want {
			t.Errorf("softStaleTickFor(%d) = %v, want %v", tc.threshold, got, tc.want)
		}
	}
}

// threadSafeBuffer is a tiny io.Writer wrapper around a strings.Builder
// shielded by a mutex. slog can write concurrently from multiple
// goroutines (notably the watchdog's emit log fired off a ticker); a
// raw strings.Builder would trip the race detector.
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// noopStream is a non-blocking EventStream stand-in for watchdog tests.
// The watchdog does not consume the bridge's main channel — it only
// reads LastEventUnix and emits synthetic events back through it. Run()
// is therefore parked on Next forever; we just need the channel to
// exist so SubscribeStream + EmitSynthetic still work.
type noopStream struct{}

func (noopStream) Next(ctx context.Context) (domain.Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (noopStream) Ack(domain.EventID) error { return nil }
func (noopStream) ResumeFrom(uint64) error  { return nil }

// fakeProbe is the WebsocketProbe stand-in. The boolean is atomic so
// the test goroutine can flip it while the watchdog goroutine reads it
// concurrently — synctest serializes scheduling at blocking points but
// the race detector still flags the unguarded plain-field access.
type fakeProbe struct{ up atomic.Bool }

func (p *fakeProbe) WebsocketConnected() bool { return p.up.Load() }

// drainSyntheticEvents collects every state.softStale / state.restored
// event currently pending on the subscriber channel. Non-blocking — the
// caller is responsible for synctest.Wait() ordering.
func drainSyntheticEvents(sub <-chan app.Event) []app.Event {
	var out []app.Event
	for {
		select {
		case e := <-sub:
			out = append(out, e)
		default:
			return out
		}
	}
}

// TestSoftStale_DisabledReturnsImmediately asserts that ThresholdSec<=0
// short-circuits the watchdog without spawning any tickers — disabled
// must mean *zero* CPU cost on the daemon, not a quiet goroutine. With
// threshold=0, the function returns before touching Bridge or Probe, so
// we pass nil deps to prove the early-out is real (not a lucky no-op).
func TestSoftStale_DisabledReturnsImmediately(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deps := SoftStaleDeps{
		Bridge:       nil,
		Probe:        nil,
		ThresholdSec: 0,
		Log:          slog.Default(),
	}

	done := make(chan struct{})
	go func() {
		runSoftStaleWatchdog(ctx, deps)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not return on threshold=0")
	}
}

// TestSoftStale_HealthyToStaleToRestored drives the full state machine
// under synctest virtual time:
//
//  1. Seed lastEvent = virtual now, then advance past the threshold
//     while websocket stays up → expect one state.softStale emit.
//  2. Push a real event through the bridge (resets lastEvent to virtual
//     now) → expect one state.restored emit on the next tick.
//
// Spec 110g FR-008 + FR-010.
func TestSoftStale_HealthyToStaleToRestored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream := newReplayStream()
		bridge := app.NewEventBridge(stream, slog.Default())
		bridge.SetProfile("watchdog-test")
		go bridge.Run()
		t.Cleanup(bridge.Close)

		sub, cancelSub := bridge.SubscribeStream(
			[]string{"state.softStale", "state.restored"}, 8)
		defer cancelSub()

		// Push a first event so LastEventUnix > 0 (otherwise the
		// watchdog stays inert per the pre-pair guard).
		stream.push(newMessageEvent("seed", time.Now()))
		synctest.Wait() // bridge.Run dispatches; lastEvent now = virtual now

		probe := &fakeProbe{}
		probe.up.Store(true)
		deps := SoftStaleDeps{
			Bridge:       bridge,
			Probe:        probe,
			Profile:      "watchdog-test",
			ThresholdSec: 60,
			NowFn:        time.Now,
			Log:          slog.Default(),
		}
		go runSoftStaleWatchdog(ctx, deps)

		// Tick is 60/3 = 20s. First tick after first real event lands
		// in stateHealthy — that's a stateUnknown→stateHealthy
		// transition, which the spec FR-009 says MUST NOT emit. So we
		// expect zero events after the first tick.
		<-time.After(25 * time.Second)
		synctest.Wait()
		if evts := drainSyntheticEvents(sub); len(evts) != 0 {
			t.Fatalf("after first tick, got %d events, want 0 (suppress-first-emit)", len(evts))
		}

		// Advance past threshold without any new events → softStale.
		<-time.After(70 * time.Second)
		synctest.Wait()

		evts := drainSyntheticEvents(sub)
		if len(evts) != 1 {
			t.Fatalf("after stale window, got %d events, want 1: %+v", len(evts), evts)
		}
		if evts[0].Type != "state.softStale" {
			t.Fatalf("stale emit Type=%q, want state.softStale", evts[0].Type)
		}
		che, ok := evts[0].Payload.(domain.ConnectivityHealthEvent)
		if !ok {
			t.Fatalf("payload type %T, want ConnectivityHealthEvent", evts[0].Payload)
		}
		if che.State != domain.HealthSoftStale {
			t.Errorf("softStale State=%v, want HealthSoftStale", che.State)
		}
		if !strings.Contains(che.Detail, "thresholdSec=60") {
			t.Errorf("Detail %q missing thresholdSec=60", che.Detail)
		}

		// Now push a real inbound event → bridge bumps lastEvent → next
		// tick should classify as healthy and emit state.restored.
		stream.push(newMessageEvent("recovery", time.Now()))
		synctest.Wait()
		<-time.After(25 * time.Second)
		synctest.Wait()

		evts = drainSyntheticEvents(sub)
		if len(evts) != 1 {
			t.Fatalf("after recovery, got %d events, want 1: %+v", len(evts), evts)
		}
		if evts[0].Type != "state.restored" {
			t.Fatalf("restored emit Type=%q, want state.restored", evts[0].Type)
		}
	})
}

// TestSoftStale_WebsocketDownReArms verifies that a hard disconnect
// resets the watchdog to stateUnknown — so the first event after
// reconnect doesn't spuriously emit a state.restored when there was no
// preceding state.softStale. Spec 110g FR-011.
func TestSoftStale_WebsocketDownReArms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream := newReplayStream()
		bridge := app.NewEventBridge(stream, slog.Default())
		bridge.SetProfile("watchdog-test")
		go bridge.Run()
		t.Cleanup(bridge.Close)

		sub, cancelSub := bridge.SubscribeStream(
			[]string{"state.softStale", "state.restored"}, 8)
		defer cancelSub()

		stream.push(newMessageEvent("seed", time.Now()))
		synctest.Wait()

		probe := &fakeProbe{} // start with ws down (zero-value atomic.Bool = false)
		deps := SoftStaleDeps{
			Bridge:       bridge,
			Probe:        probe,
			Profile:      "watchdog-test",
			ThresholdSec: 60,
			NowFn:        time.Now,
			Log:          slog.Default(),
		}
		go runSoftStaleWatchdog(ctx, deps)

		// Many ticks pass with ws down — must emit nothing.
		<-time.After(200 * time.Second)
		synctest.Wait()
		if evts := drainSyntheticEvents(sub); len(evts) != 0 {
			t.Fatalf("ws-down emitted %d events, want 0", len(evts))
		}

		// Bring ws back up while staleness > threshold. The watchdog
		// was just re-armed (stateUnknown) on every ws-down tick, so
		// the first classification is suppressed — no spurious
		// state.softStale despite staleSeconds being huge. After this
		// tick the internal state is silently stateStale.
		probe.up.Store(true)
		<-time.After(25 * time.Second)
		synctest.Wait()
		if evts := drainSyntheticEvents(sub); len(evts) != 0 {
			t.Fatalf("first tick after re-arm emitted %d events, want 0 (suppress-first-after-rearm)", len(evts))
		}

		// Push a real event so lastEvent resets to virtual now →
		// staleSeconds drops to ~0 → next tick classifies as
		// stateHealthy → emit one state.restored. This proves the
		// re-arm did NOT block subsequent transitions.
		stream.push(newMessageEvent("recovery", time.Now()))
		synctest.Wait()
		<-time.After(25 * time.Second)
		synctest.Wait()
		evts := drainSyntheticEvents(sub)
		if len(evts) != 1 || evts[0].Type != "state.restored" {
			t.Fatalf("stale→healthy after recovery: got %d events (%+v), want 1 state.restored", len(evts), evts)
		}
	})
}

// replayStream is a synctest-safe EventStream that lets a test push
// arbitrary domain.Events into the bridge in FIFO order. Differs from
// the app package's fakeStream only in being importable from cmd/wad —
// both implement the same contract (block on Next when empty, signal
// on enqueue).
type replayStream struct {
	mu     sync.Mutex
	events []domain.Event
	signal chan struct{}
}

func newReplayStream() *replayStream {
	return &replayStream{signal: make(chan struct{}, 1)}
}

func (r *replayStream) Next(ctx context.Context) (domain.Event, error) {
	for {
		r.mu.Lock()
		if len(r.events) > 0 {
			evt := r.events[0]
			r.events = r.events[1:]
			r.mu.Unlock()
			return evt, nil
		}
		r.mu.Unlock()
		select {
		case <-r.signal:
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *replayStream) Ack(domain.EventID) error { return nil }
func (r *replayStream) ResumeFrom(uint64) error  { return nil }

func (r *replayStream) push(evt domain.Event) {
	r.mu.Lock()
	r.events = append(r.events, evt)
	r.mu.Unlock()
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

// newMessageEvent builds a minimal domain.MessageEvent for stream
// injection. Body content is irrelevant — the watchdog only cares that
// *some* event landed so lastEventUnix moves forward.
func newMessageEvent(id string, ts time.Time) domain.MessageEvent {
	return domain.MessageEvent{
		ID:   domain.EventID(id),
		TS:   ts,
		From: domain.MustJID("12025550100@s.whatsapp.net"),
	}
}

// TestParseSoftStaleRecover covers the opt-in recover flag (spec 110g
// recover extension): truthy tokens enable, everything else — including
// unset/empty/garbage — leaves the watchdog detect-only.
func TestParseSoftStaleRecover(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"Yes", true},
		{"on", true},
		{"  1  ", true},
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"2", false},
		{"enable", false},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := ParseSoftStaleRecover(c.raw); got != c.want {
			t.Errorf("ParseSoftStaleRecover(%q)=%v, want %v", c.raw, got, c.want)
		}
	}
}

// TestSoftStale_RecoverFiresOncePerEdgeWithCooldown verifies the spec
// 110g recover extension end to end under synctest virtual time:
//
//  1. The initial unknown->healthy classification must NOT recover
//     (mirrors the suppress-first-emit rule, FR-009).
//  2. A genuine healthy->stale edge invokes deps.Recover exactly once.
//  3. A second stale edge inside the cooldown window is suppressed, so
//     the recover count stays at one.
func TestSoftStale_RecoverFiresOncePerEdgeWithCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream := newReplayStream()
		bridge := app.NewEventBridge(stream, slog.Default())
		bridge.SetProfile("recover-test")
		go bridge.Run()
		t.Cleanup(bridge.Close)

		// Seed so LastEventUnix > 0 (pre-pair guard) — virtual now.
		stream.push(newMessageEvent("seed", time.Now()))
		synctest.Wait()

		probe := &fakeProbe{}
		probe.up.Store(true)

		var recoverCalls atomic.Int32
		deps := SoftStaleDeps{
			Bridge:             bridge,
			Probe:              probe,
			Profile:            "recover-test",
			ThresholdSec:       60,
			NowFn:              time.Now,
			Log:                slog.Default(),
			Recover:            func(context.Context) error { recoverCalls.Add(1); return nil },
			RecoverCooldownSec: 300,
		}
		go runSoftStaleWatchdog(ctx, deps)

		// First tick (20s) classifies unknown->healthy — silent, no recover.
		<-time.After(25 * time.Second)
		synctest.Wait()
		if n := recoverCalls.Load(); n != 0 {
			t.Fatalf("after first tick recoverCalls=%d, want 0 (no recover on boot classification)", n)
		}

		// Advance past the threshold with the socket up → healthy->stale
		// edge → exactly one reconnect.
		<-time.After(70 * time.Second)
		synctest.Wait()
		if n := recoverCalls.Load(); n != 1 {
			t.Fatalf("after stale window recoverCalls=%d, want 1", n)
		}

		// A real event restores healthy on the next tick.
		stream.push(newMessageEvent("recovery", time.Now()))
		synctest.Wait()
		<-time.After(25 * time.Second)
		synctest.Wait()

		// Cross the threshold again — still inside the 300s cooldown since
		// the first reconnect → suppressed, count stays 1.
		<-time.After(70 * time.Second)
		synctest.Wait()
		if n := recoverCalls.Load(); n != 1 {
			t.Fatalf("second stale edge inside cooldown: recoverCalls=%d, want 1 (suppressed)", n)
		}
	})
}

// TestParseSoftStaleBackfill covers the opt-in backfill flag (spec 110g
// backfill extension): truthy tokens enable, everything else — including
// unset/empty/garbage — leaves recovery reconnect-only.
func TestParseSoftStaleBackfill(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"On", true},
		{"  yes  ", true},
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"2", false},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := ParseSoftStaleBackfill(c.raw); got != c.want {
			t.Errorf("ParseSoftStaleBackfill(%q)=%v, want %v", c.raw, got, c.want)
		}
	}
}

// TestSoftStale_BackfillFiresAfterRecover verifies the spec 110g backfill
// extension: on a genuine healthy->stale edge the watchdog runs Recover
// and then, once it succeeds, runs Backfill — exactly once each, gated by
// the same edge as recovery (no fire on the boot classification).
func TestSoftStale_BackfillFiresAfterRecover(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream := newReplayStream()
		bridge := app.NewEventBridge(stream, slog.Default())
		bridge.SetProfile("backfill-test")
		go bridge.Run()
		t.Cleanup(bridge.Close)

		stream.push(newMessageEvent("seed", time.Now()))
		synctest.Wait()

		probe := &fakeProbe{}
		probe.up.Store(true)

		var recoverCalls, backfillCalls atomic.Int32
		deps := SoftStaleDeps{
			Bridge:             bridge,
			Probe:              probe,
			Profile:            "backfill-test",
			ThresholdSec:       60,
			NowFn:              time.Now,
			Log:                slog.Default(),
			Recover:            func(context.Context) error { recoverCalls.Add(1); return nil },
			Backfill:           func(context.Context) error { backfillCalls.Add(1); return nil },
			RecoverCooldownSec: 300,
		}
		go runSoftStaleWatchdog(ctx, deps)

		// First tick (20s) classifies unknown->healthy — silent, neither fires.
		<-time.After(25 * time.Second)
		synctest.Wait()
		if r, b := recoverCalls.Load(), backfillCalls.Load(); r != 0 || b != 0 {
			t.Fatalf("after first tick recover=%d backfill=%d, want 0/0", r, b)
		}

		// Cross the threshold with the socket up → healthy->stale edge →
		// one reconnect, then one backfill.
		<-time.After(70 * time.Second)
		synctest.Wait()
		if r := recoverCalls.Load(); r != 1 {
			t.Fatalf("recoverCalls=%d, want 1", r)
		}
		if b := backfillCalls.Load(); b != 1 {
			t.Fatalf("backfillCalls=%d, want 1 (backfill must follow a successful recover)", b)
		}
	})
}

// TestSoftStale_BackfillSkippedWhenRecoverFails proves the backfill never
// runs over a still-zombie link: when the reconnect returns an error the
// goroutine returns before backfill, so backfillCalls stays zero.
func TestSoftStale_BackfillSkippedWhenRecoverFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream := newReplayStream()
		bridge := app.NewEventBridge(stream, slog.Default())
		bridge.SetProfile("backfill-fail-test")
		go bridge.Run()
		t.Cleanup(bridge.Close)

		stream.push(newMessageEvent("seed", time.Now()))
		synctest.Wait()

		probe := &fakeProbe{}
		probe.up.Store(true)

		var backfillCalls atomic.Int32
		deps := SoftStaleDeps{
			Bridge:             bridge,
			Probe:              probe,
			Profile:            "backfill-fail-test",
			ThresholdSec:       60,
			NowFn:              time.Now,
			Log:                slog.Default(),
			Recover:            func(context.Context) error { return errors.New("reconnect boom") },
			Backfill:           func(context.Context) error { backfillCalls.Add(1); return nil },
			RecoverCooldownSec: 300,
		}
		go runSoftStaleWatchdog(ctx, deps)

		<-time.After(25 * time.Second) // unknown->healthy, silent
		synctest.Wait()
		<-time.After(70 * time.Second) // healthy->stale edge: recover fails
		synctest.Wait()

		if b := backfillCalls.Load(); b != 0 {
			t.Fatalf("backfillCalls=%d, want 0 (no backfill over a failed reconnect)", b)
		}
	})
}
