package whatsmeow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/appstate"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

func resyncAdapter(t *testing.T) (*Adapter, *fakeWhatsmeowClient) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, nil, discardLogger(), fixedNowFn)
	t.Cleanup(func() { _ = a.Close() })
	return a, fc
}

// TestResyncAppStateFullDiscardsLocal — the repair case. A hash mismatch
// cannot be fixed by a catch-up, so full MUST be forwarded, and
// onlyIfNotSynced MUST be false: the whole point is re-fetching a
// collection we HAVE synced, because what we have is wrong.
func TestResyncAppStateFullDiscardsLocal(t *testing.T) {
	a, fc := resyncAdapter(t)

	if err := a.ResyncAppState(context.Background(), "regular_high", true); err != nil {
		t.Fatalf("ResyncAppState: %v", err)
	}
	if got := len(fc.FetchAppStateCalls); got != 1 {
		t.Fatalf("FetchAppState calls = %d, want 1", got)
	}
	c := fc.FetchAppStateCalls[0]
	if c.Name != appstate.WAPatchRegularHigh {
		t.Errorf("collection = %q, want regular_high", c.Name)
	}
	if !c.Full {
		t.Error("full=false — a catch-up cannot repair a mismatching LTHash")
	}
	if c.OnlyIfNotSynced {
		t.Error("onlyIfNotSynced=true would skip the very collection we are repairing")
	}
}

// TestResyncAppStateIncremental — full=false is still forwarded, for the
// cheap catch-up case.
func TestResyncAppStateIncremental(t *testing.T) {
	a, fc := resyncAdapter(t)
	if err := a.ResyncAppState(context.Background(), "regular_low", false); err != nil {
		t.Fatalf("ResyncAppState: %v", err)
	}
	if fc.FetchAppStateCalls[0].Full {
		t.Error("full=true was forwarded for an incremental resync")
	}
}

// TestResyncAppStateRejectsUnknownCollection — an arbitrary string must
// not reach the server. The error names the valid set so the caller can
// fix it without reading source.
func TestResyncAppStateRejectsUnknownCollection(t *testing.T) {
	a, fc := resyncAdapter(t)
	err := a.ResyncAppState(context.Background(), "regular_highh", true)
	if err == nil {
		t.Fatal("a typo'd collection was accepted")
	}
	if !strings.Contains(err.Error(), "regular_high") {
		t.Errorf("error does not list the valid collections: %v", err)
	}
	if len(fc.FetchAppStateCalls) != 0 {
		t.Error("an unknown collection still reached the server")
	}
}

// TestResyncAppStateRefusesDisconnected — a resync is a network round
// trip; refusing offline beats a confusing transport error.
func TestResyncAppStateRefusesDisconnected(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = false
	a := openWithClient(fc, nil, discardLogger(), fixedNowFn)
	t.Cleanup(func() { _ = a.Close() })

	if err := a.ResyncAppState(context.Background(), "regular_high", true); !errors.Is(err, domain.ErrDisconnected) {
		t.Fatalf("want ErrDisconnected, got %v", err)
	}
	if len(fc.FetchAppStateCalls) != 0 {
		t.Error("a resync was attempted while disconnected")
	}
}

// TestResyncAppStatePropagatesFailure — the caller must learn the repair
// did not work; swallowing it would leave every app-state write broken
// while the RPC reported success.
func TestResyncAppStatePropagatesFailure(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = errors.New("server said no")
	err := a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || !strings.Contains(err.Error(), "server said no") {
		t.Fatalf("failure not propagated: %v", err)
	}
}

// TestAppStateCollectionsMatchesWhatsmeow — the accepted set is derived
// from appstate.AllPatchNames, so a whatsmeow addition cannot leave this
// list quietly short and make a valid collection unrepairable.
func TestAppStateCollectionsMatchesWhatsmeow(t *testing.T) {
	got := AppStateCollections()
	if len(got) != len(appstate.AllPatchNames) {
		t.Fatalf("collections = %v, want %d entries", got, len(appstate.AllPatchNames))
	}
	for _, want := range appstate.AllPatchNames {
		found := false
		for _, g := range got {
			if g == string(want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing from AppStateCollections()", want)
		}
	}
}
