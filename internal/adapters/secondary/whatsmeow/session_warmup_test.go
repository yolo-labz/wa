package whatsmeow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
)

// The real store must satisfy the optional capability, or WarmupSince
// silently falls back to per-boot values in production while every test
// here passes against the fake.
var _ warmupClock = (*sqlitestore.Store)(nil)

// fakeWarmupClock is the production shape: sqlitestore.Store satisfies
// pairingClock and warmupClock at once. It embeds fakePairingClock rather
// than restating PairedAt/SetPairedAt/Container/Close, so the two fakes
// cannot drift apart on the half they share.
type fakeWarmupClock struct {
	fakePairingClock

	wmu         sync.Mutex
	warmupTS    time.Time
	warmupKnown bool
	readErr     error
	writeErr    error
	writes      []time.Time
}

func (f *fakeWarmupClock) WarmupEpoch(context.Context) (time.Time, bool, error) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if f.readErr != nil {
		return time.Time{}, false, f.readErr
	}
	return f.warmupTS, f.warmupKnown, nil
}

func (f *fakeWarmupClock) SetWarmupEpoch(_ context.Context, t time.Time) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.writes = append(f.writes, t)
	if f.writeErr != nil {
		return f.writeErr
	}
	f.warmupTS, f.warmupKnown = t, !t.IsZero()
	return nil
}

func (f *fakeWarmupClock) writtenEpochs() []time.Time {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	return append([]time.Time(nil), f.writes...)
}

// TestWarmupSince_AdoptsAndPersistsOnce is issue #368 itself. A paired
// session with an unknown pairing instant must adopt an epoch ONCE and then
// report the same value across restarts. Before the fix each boot answered
// time.Now(), re-pinning warmup day 0 forever.
func TestWarmupSince_AdoptsAndPersistsOnce(t *testing.T) {
	clock := &fakeWarmupClock{} // paired instant unknown — a pre-#311 session
	a := pairedAdapter(t, clock)
	ctx := context.Background()

	firstBoot := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	got, paired := a.WarmupSince(ctx, firstBoot)
	if !paired {
		t.Fatal("paired reported false for a paired device")
	}
	if !got.Equal(firstBoot) {
		t.Errorf("first boot: got %s, want %s", got, firstBoot)
	}
	if w := clock.writtenEpochs(); len(w) != 1 {
		t.Fatalf("first boot must persist exactly one epoch, got %d", len(w))
	}

	// A later restart must reuse the stored epoch, NOT its own clock.
	laterBoot := firstBoot.Add(72 * time.Hour)
	got2, _ := a.WarmupSince(ctx, laterBoot)
	if !got2.Equal(firstBoot) {
		t.Errorf("restart: got %s, want the stored %s — warmup re-pinned to now", got2, firstBoot)
	}
	if w := clock.writtenEpochs(); len(w) != 1 {
		t.Errorf("restart must not re-persist; writes = %d", len(w))
	}
}

// TestWarmupSince_PrefersRealPairingInstant — when the handshake time IS
// known it wins outright, and nothing is written. The adopted epoch is a
// fallback, never an override.
func TestWarmupSince_PrefersRealPairingInstant(t *testing.T) {
	paired := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakeWarmupClock{fakePairingClock: fakePairingClock{ts: paired, known: true}}
	a := pairedAdapter(t, clock)
	a.loadPairedAt(context.Background())

	got, isPaired := a.WarmupSince(context.Background(), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	if !isPaired {
		t.Fatal("paired reported false")
	}
	if !got.Equal(paired) {
		t.Errorf("got %s, want the real pairing instant %s", got, paired)
	}
	if w := clock.writtenEpochs(); len(w) != 0 {
		t.Errorf("must not write an epoch when the pairing instant is known; writes = %v", w)
	}
}

// TestWarmupSince_UnpairedStartsNow — a device that genuinely has not
// paired must warm up from now and must NOT persist an epoch, or the next
// real handshake would inherit a ladder it never started.
func TestWarmupSince_UnpairedStartsNow(t *testing.T) {
	clock := &fakeWarmupClock{}
	fake := newFakeClient() // no Device → unpaired
	a := openWithClient(fake, nil, discardLogger(), advancingNow())
	a.session = clock
	t.Cleanup(func() { _ = a.Close() })

	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	got, paired := a.WarmupSince(context.Background(), now)
	if paired {
		t.Error("paired reported true with no device")
	}
	if !got.Equal(now) {
		t.Errorf("got %s, want now %s", got, now)
	}
	if w := clock.writtenEpochs(); len(w) != 0 {
		t.Errorf("unpaired must not persist an epoch; writes = %v", w)
	}
}

// TestWarmupSince_DegradesOnStoreFailure — a store that cannot answer or
// record yields a per-boot value, which is exactly the pre-fix behaviour.
// Degraded, not broken: a daemon that refuses to start because it could not
// write a rate-limiter hint would be a worse bug than the one being fixed.
func TestWarmupSince_DegradesOnStoreFailure(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	t.Run("read error", func(t *testing.T) {
		a := pairedAdapter(t, &fakeWarmupClock{readErr: errors.New("db gone")})
		got, paired := a.WarmupSince(context.Background(), now)
		if !paired || !got.Equal(now) {
			t.Errorf("got (%s, %v), want (%s, true)", got, paired, now)
		}
	})

	t.Run("write error", func(t *testing.T) {
		a := pairedAdapter(t, &fakeWarmupClock{writeErr: errors.New("readonly")})
		got, paired := a.WarmupSince(context.Background(), now)
		if !paired || !got.Equal(now) {
			t.Errorf("got (%s, %v), want (%s, true)", got, paired, now)
		}
	})

	t.Run("container without the capability", func(t *testing.T) {
		// The same shape as a stub container in another test package.
		a := pairedAdapter(t, &fakePairingClock{})
		got, paired := a.WarmupSince(context.Background(), now)
		if !paired || !got.Equal(now) {
			t.Errorf("got (%s, %v), want (%s, true)", got, paired, now)
		}
	})
}
