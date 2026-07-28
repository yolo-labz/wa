package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// newTestEventsStore opens a throwaway durable ring, torn down with the test.
func newTestEventsStore(t *testing.T) (context.Context, *sqliteevents.Store) {
	t.Helper()
	ctx := context.Background()
	store, err := sqliteevents.Open(ctx, filepath.Join(t.TempDir(), "events.db"), 100)
	if err != nil {
		t.Fatalf("open events store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return ctx, store
}

// TestSocketReplayShim_SelectorRoundTrip pins the FR-060-over-FR-061 wire
// end to end: the pump stores an event's chat/sender/body selectors, and
// the socket shim hands them back on replay. Break either half and a
// resumed `--chats`/`--senders`/`--body-re` subscription silently matches
// nothing, because those fields are `json:"-"` and so absent from the
// payload the ring stores.
func TestSocketReplayShim_SelectorRoundTrip(t *testing.T) {
	ctx, store := newTestEventsStore(t)

	src := &fakePumpSource{ch: make(chan app.Event, 4)}
	stop := startEventsPump(ctx, src, store, quietLogger())

	src.ch <- app.Event{
		Type:    "message",
		Seq:     7,
		Payload: map[string]any{"id": "M1"},
		Chat:    "friend@s.whatsapp.net",
		Sender:  "friend@s.whatsapp.net",
		Body:    "orbital mechanics",
	}

	shim := &socketReplayShim{store: store}
	var recs []socketReplayRecord
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, rErr := shim.Replay(ctx, 0, 10)
		if rErr != nil {
			t.Fatalf("replay: %v", rErr)
		}
		if len(got) == 1 {
			recs = append(recs, socketReplayRecord{
				seq: got[0].Seq, kind: got[0].Kind,
				chat: got[0].Chat, sender: got[0].Sender, body: got[0].Body,
				data: string(got[0].Data),
			})
			break
		}
		<-time.After(10 * time.Millisecond)
	}
	stop()

	if len(recs) != 1 {
		t.Fatal("pump never appended the event to the ring")
	}
	got := recs[0]
	if got.seq != 7 || got.kind != "message" {
		t.Errorf("seq/kind = %d/%s, want 7/message", got.seq, got.kind)
	}
	if got.chat != "friend@s.whatsapp.net" || got.sender != "friend@s.whatsapp.net" {
		t.Errorf("chat/sender = %s/%s, want friend@s.whatsapp.net both", got.chat, got.sender)
	}
	if got.body != "orbital mechanics" {
		t.Errorf("body = %q, want %q", got.body, "orbital mechanics")
	}
	if got.data != `{"id":"M1"}` {
		t.Errorf("payload = %s, want {\"id\":\"M1\"}", got.data)
	}
}

// socketReplayRecord is a local flattening of socket.ReplayRecord so the
// assertions read without importing the socket package into cmd/wad tests.
type socketReplayRecord struct {
	seq                      int64
	kind, chat, sender, body string
	data                     string
}

// TestSocketReplayShim_LegacyRowDecodesToNoSelectors pins the behaviour on
// rows written before the pump stored selectors: they replay with empty
// selectors, so a filtered subscription skips them rather than receiving
// events it explicitly excluded.
func TestSocketReplayShim_LegacyRowDecodesToNoSelectors(t *testing.T) {
	ctx, store := newTestEventsStore(t)

	// No UntrustedJSON — exactly what every pre-#304 row looks like.
	if err := store.Append(ctx, app.EventRecord{
		Seq: 3, Kind: "message", TrustedJSON: []byte(`{"id":"M0"}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	recs, err := (&socketReplayShim{store: store}).Replay(ctx, 0, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("replayed %d rows, want 1", len(recs))
	}
	if recs[0].Chat != "" || recs[0].Sender != "" || recs[0].Body != "" {
		t.Errorf("legacy row produced selectors %+v, want all empty", recs[0])
	}
	if string(recs[0].Data) != `{"id":"M0"}` {
		t.Errorf("payload = %s, want the stored row", recs[0].Data)
	}
}

// TestSocketReplayShim_OldestSeq pins the eviction signal the socket resume
// path reads: the shim reports the ring's oldest retained seq, which is what
// tells a reconnecting client its cursor fell off the back.
func TestSocketReplayShim_OldestSeq(t *testing.T) {
	ctx, store := newTestEventsStore(t)

	shim := &socketReplayShim{store: store}
	if oldest, oErr := shim.OldestSeq(ctx); oErr != nil || oldest != 0 {
		t.Fatalf("empty ring OldestSeq = %d, %v; want 0, nil", oldest, oErr)
	}

	for _, seq := range []int64{11, 12} {
		if err := store.Append(ctx, app.EventRecord{Seq: seq, Kind: "message", TrustedJSON: []byte(`{}`)}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	oldest, err := shim.OldestSeq(ctx)
	if err != nil {
		t.Fatalf("OldestSeq: %v", err)
	}
	if oldest != 11 {
		t.Fatalf("OldestSeq = %d, want 11", oldest)
	}
}
