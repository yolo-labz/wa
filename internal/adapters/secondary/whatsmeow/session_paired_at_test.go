package whatsmeow

// session_paired_at_test.go pins issue #311: health.sessionSince used to
// be minted with time.Now() inside liveSession, so every poll reported the
// pairing as having started at the instant of the poll. A field whose only
// job is to say how long the pairing has survived instead said nothing —
// while reading as evidence.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waEvents "go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
)

// *sqlitestore.Store is the sessionContainer the composition root wires in,
// and the adapter only learns the pairing instant if it ALSO satisfies the
// optional pairingClock. Assert the wiring here so a signature drift on
// either side breaks the build instead of silently degrading every session
// back to "unknown".
var _ pairingClock = (*sqlitestore.Store)(nil)

// fakePairingClock is a sessionContainer that also implements pairingClock,
// standing in for sqlitestore.Store without touching SQLite.
type fakePairingClock struct {
	mu       sync.Mutex
	ts       time.Time
	known    bool
	readErr  error
	writeErr error
	writes   []time.Time
}

func (f *fakePairingClock) Container() *sqlstore.Container { return nil }
func (f *fakePairingClock) Close() error                   { return nil }

func (f *fakePairingClock) PairedAt(context.Context) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return time.Time{}, false, f.readErr
	}
	return f.ts, f.known, nil
}

func (f *fakePairingClock) SetPairedAt(_ context.Context, t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, t)
	if f.writeErr != nil {
		return f.writeErr
	}
	f.ts, f.known = t, !t.IsZero()
	return nil
}

func (f *fakePairingClock) written() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.writes...)
}

// advancingNow returns a clock that moves one minute per call, so a
// timestamp minted at call time is trivially distinguishable from one read
// out of the store.
func advancingNow() func() time.Time {
	var mu sync.Mutex
	cur := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		cur = cur.Add(time.Minute)
		return cur
	}
}

// pairedAdapter builds an adapter with a paired device and the given
// session container.
func pairedAdapter(t *testing.T, session sessionContainer) *Adapter {
	t.Helper()
	fake := newFakeClient()
	fake.Device = fakeDeviceWithJID(t, "5511999999999@s.whatsapp.net")
	a := openWithClient(fake, nil, discardLogger(), advancingNow())
	a.session = session
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestLoad_CreatedAtIsThePairingInstantNotNow is the regression itself: two
// polls minutes apart must report the SAME sessionSince, and it must be the
// stored pairing instant rather than either poll's clock reading.
func TestLoad_CreatedAtIsThePairingInstantNotNow(t *testing.T) {
	paired := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakePairingClock{ts: paired, known: true}
	a := pairedAdapter(t, clock)
	a.loadPairedAt(context.Background())

	first, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	second, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}

	if !first.CreatedAt().Equal(paired) {
		t.Fatalf("CreatedAt = %s, want the stored pairing instant %s", first.CreatedAt(), paired)
	}
	if !second.CreatedAt().Equal(first.CreatedAt()) {
		t.Fatalf("CreatedAt moved between polls: %s then %s", first.CreatedAt(), second.CreatedAt())
	}
	if !first.IsLoggedIn() {
		t.Fatal("IsLoggedIn = false for a paired device")
	}
}

// TestLoad_UnknownPairingLeavesCreatedAtZero covers every session paired
// before this landed: there is nothing to back-fill from, so the field is
// absent. The session must still report as logged in.
func TestLoad_UnknownPairingLeavesCreatedAtZero(t *testing.T) {
	// No pairingClock at all — the same shape as a container that has
	// never recorded a timestamp.
	a := pairedAdapter(t, nil)

	sess, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sess.CreatedAt().IsZero() {
		t.Fatalf("CreatedAt = %s, want zero (unknown) — a minted value reads as evidence", sess.CreatedAt())
	}
	if !sess.IsLoggedIn() {
		t.Fatal("IsLoggedIn = false; an unknown pairing time must not un-pair the session")
	}
}

// TestPairSuccessRecordsPairingInstant asserts the daemon captures the one
// moment it can know the handshake completed, and that the recorded instant
// is what a subsequent Load reports.
func TestPairSuccessRecordsPairingInstant(t *testing.T) {
	clock := &fakePairingClock{}
	a := pairedAdapter(t, clock)

	if !a.handleWAEvent(&waEvents.PairSuccess{}) {
		t.Fatal("handleWAEvent(PairSuccess) = false, want the event handled")
	}

	writes := clock.written()
	if len(writes) != 1 {
		t.Fatalf("SetPairedAt calls = %d, want 1", len(writes))
	}
	if writes[0].IsZero() {
		t.Fatal("recorded pairing instant is zero")
	}

	sess, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sess.CreatedAt().Equal(writes[0]) {
		t.Fatalf("CreatedAt = %s, want the recorded pairing instant %s", sess.CreatedAt(), writes[0])
	}
}

// TestClearSessionResetsPairingInstant: the timestamp describes the device
// that just went away, so unlinking must take it along — otherwise a later
// pairing inherits the old session's age.
func TestClearSessionResetsPairingInstant(t *testing.T) {
	clock := &fakePairingClock{ts: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC), known: true}
	a := pairedAdapter(t, clock)
	a.loadPairedAt(context.Background())

	if err := a.Clear(context.Background()); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	writes := clock.written()
	if len(writes) != 1 || !writes[0].IsZero() {
		t.Fatalf("SetPairedAt writes = %v, want exactly one zero time", writes)
	}
	sess, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("Load after Clear: %v", err)
	}
	if !sess.CreatedAt().IsZero() {
		t.Fatalf("CreatedAt = %s after Clear, want zero", sess.CreatedAt())
	}
}

// TestLoadPairedAtSurvivesStoreFailure: a daemon that cannot read one
// cosmetic timestamp must still serve messages, reporting the field as
// absent rather than refusing to start.
func TestLoadPairedAtSurvivesStoreFailure(t *testing.T) {
	clock := &fakePairingClock{readErr: errors.New("disk on fire")}
	a := pairedAdapter(t, clock)
	a.loadPairedAt(context.Background())

	sess, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sess.CreatedAt().IsZero() {
		t.Fatalf("CreatedAt = %s after a failed read, want zero", sess.CreatedAt())
	}
	if !sess.IsLoggedIn() {
		t.Fatal("IsLoggedIn = false; a store read failure must not un-pair the session")
	}
}

// TestRecordPairedAtCachesDespiteWriteFailure: the running process must
// report the truth it just observed even when persisting it fails; only the
// next restart falls back to unknown.
func TestRecordPairedAtCachesDespiteWriteFailure(t *testing.T) {
	clock := &fakePairingClock{writeErr: errors.New("read-only filesystem")}
	a := pairedAdapter(t, clock)

	want := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	a.recordPairedAt(context.Background(), want)

	sess, err := a.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sess.CreatedAt().Equal(want) {
		t.Fatalf("CreatedAt = %s, want %s cached despite the write failure", sess.CreatedAt(), want)
	}
}
