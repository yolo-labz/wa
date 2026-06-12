package rest

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEventStream is a minimal EventStream double for handler tests.
// Tests publish via Publish; the Subscribe channel buffers each event
// until the SSE handler drains it.
type fakeEventStream struct {
	mu  sync.Mutex
	chs []chan Event
	seq int64
}

func newFakeEventStream() *fakeEventStream {
	return &fakeEventStream{}
}

func (f *fakeEventStream) SubscribeStream(_ []string, bufSize int) (<-chan Event, func()) {
	ch := make(chan Event, bufSize)
	f.mu.Lock()
	f.chs = append(f.chs, ch)
	f.mu.Unlock()
	cancel := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, candidate := range f.chs {
			if candidate == ch {
				f.chs = append(f.chs[:i], f.chs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

// Publish broadcasts an event to all currently-subscribed channels.
// Non-blocking; full channels drop the event (matches the production
// EventBridge semantics).
func (f *fakeEventStream) Publish(eventType string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	data, _ := json.Marshal(payload)
	evt := Event{Seq: f.seq, Type: eventType, Data: data}
	for _, ch := range f.chs {
		select {
		case ch <- evt:
		default:
		}
	}
}

func newSSEServerForTest(t *testing.T, fes *fakeEventStream, token string) *Server {
	t.Helper()
	srv, err := NewServer(
		t.Context(), "127.0.0.1:0",
		&fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		}},
		NewEnvTokenAuth(token),
		WithLogger(discardLogger()),
		WithEventStream(fes),
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	})
	return srv
}

// TestSSE_ConnectAndReceive pins spec 110b §FR-001/002: an
// authenticated GET /v1/events returns 200 with text/event-stream,
// emits the initial `: connected` comment, then each Publish lands
// as one `id:/event:/data:` frame.
func TestSSE_ConnectAndReceive(t *testing.T) {
	fes := newFakeEventStream()
	srv := newSSEServerForTest(t, fes, "secret")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	scanner := bufio.NewScanner(resp.Body)

	// First frame is the connected comment. Look for it before
	// publishing — defends against a race where Publish lands before
	// the handler enters the for-loop.
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ": connected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("connected comment frame missing; scanner err: %v", scanner.Err())
	}

	// Publish an event from another goroutine and read the SSE frame.
	go func() {
		<-time.After(20 * time.Millisecond)
		fes.Publish("message", map[string]any{"id": "MSG-1", "body": "hi"})
	}()

	var gotID, gotEvent, gotData string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (gotID == "" || gotEvent == "" || gotData == "") {
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			gotID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			gotEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			gotData = strings.TrimPrefix(line, "data: ")
		}
	}
	if gotID != "1" {
		t.Errorf("id = %q, want 1", gotID)
	}
	if gotEvent != "message" {
		t.Errorf("event = %q, want message", gotEvent)
	}
	if !strings.Contains(gotData, `"id":"MSG-1"`) {
		t.Errorf("data = %q, want to contain MSG-1", gotData)
	}
}

// TestSSE_AuthRequired pins that an unauthenticated GET /v1/events
// returns 401.
func TestSSE_AuthRequired(t *testing.T) {
	fes := newFakeEventStream()
	srv := newSSEServerForTest(t, fes, "secret")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestSSE_NoStreamConfigured pins spec 110b §FR-003: when the server
// is constructed without WithEventStream, GET /v1/events returns 503
// with a JSON-RPC error envelope.
func TestSSE_NoStreamConfigured(t *testing.T) {
	srv, err := NewServer(
		t.Context(), "127.0.0.1:0",
		&fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		}},
		NewEnvTokenAuth("secret"),
		WithLogger(discardLogger()),
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestSSE_ClientDisconnectCancels pins that closing the request
// context (client disconnect) deregisters the subscriber so
// EventBridge does not accumulate stale waiters.
func TestSSE_ClientDisconnectCancels(t *testing.T) {
	fes := newFakeEventStream()
	srv := newSSEServerForTest(t, fes, "secret")

	clientCtx, clientCancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(clientCtx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	// Wait for the connected frame to ensure the handler is in the
	// for-select loop before we disconnect.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": connected") {
			break
		}
	}

	clientCancel()
	_ = resp.Body.Close()

	// Subscriber list should drain to zero shortly after disconnect.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fes.mu.Lock()
		n := len(fes.chs)
		fes.mu.Unlock()
		if n == 0 {
			return
		}
		<-time.After(20 * time.Millisecond)
	}
	t.Fatal("subscriber not deregistered after client disconnect")
}

// --- spec 110b v1: durable-ring replay (ARCH-01 wire) --------------------

// fakeReplayer is an in-memory EventReplayer double. Tests append rows;
// the handler tails them. newest tracks the ring head for cursor-less
// connections.
type fakeReplayer struct {
	mu   sync.Mutex
	rows []ReplayRecord
	gap  *ReplayRecord // synthetic stream.drop prefix for the next Replay
}

func (f *fakeReplayer) append(kind string, data string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	seq := int64(len(f.rows) + 1)
	f.rows = append(f.rows, ReplayRecord{Seq: seq, Kind: kind, Data: []byte(data)})
	return seq
}

func (f *fakeReplayer) NewestSeq(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.rows)), nil
}

func (f *fakeReplayer) Replay(_ context.Context, sinceSeq int64, limit int) ([]ReplayRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []ReplayRecord{}
	if f.gap != nil {
		out = append(out, *f.gap)
		f.gap = nil
	}
	for _, rec := range f.rows {
		if rec.Seq > sinceSeq && len(out) < limit {
			out = append(out, rec)
		}
	}
	return out, nil
}

func newReplaySSEServerForTest(t *testing.T, fes *fakeEventStream, fr *fakeReplayer, token string) *Server {
	t.Helper()
	srv, err := NewServer(
		t.Context(), "127.0.0.1:0",
		&fakeDispatcher{handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		}},
		NewEnvTokenAuth(token),
		WithLogger(discardLogger()),
		WithEventStream(fes),
		WithEventReplay(fr),
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	})
	return srv
}

// readFrames collects SSE (id, event, data) triples until want frames
// arrive or the deadline passes. Comment lines are skipped.
func readFrames(t *testing.T, scanner *bufio.Scanner, want int, deadline time.Duration) [][3]string {
	t.Helper()
	frames := [][3]string{}
	var cur [3]string
	end := time.Now().Add(deadline)
	for time.Now().Before(end) && len(frames) < want {
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur[0] = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur[1] = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur[2] = strings.TrimPrefix(line, "data: ")
		case line == "" && (cur[1] != "" || cur[2] != ""):
			frames = append(frames, cur)
			cur = [3]string{}
		}
	}
	return frames
}

// TestSSE_ReplayFromLastEventID pins the v1 resume contract: a client
// reconnecting with Last-Event-ID: 1 receives ring rows 2 and 3 with
// their real seqs as SSE ids.
func TestSSE_ReplayFromLastEventID(t *testing.T) {
	fes := newFakeEventStream()
	fr := &fakeReplayer{}
	fr.append("message", `{"id":"A"}`)
	fr.append("message", `{"id":"B"}`)
	fr.append("receipt", `{"id":"C"}`)
	srv := newReplaySSEServerForTest(t, fes, fr, "secret")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Last-Event-ID", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	frames := readFrames(t, bufio.NewScanner(resp.Body), 2, 3*time.Second)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2: %v", len(frames), frames)
	}
	if frames[0][0] != "2" || !strings.Contains(frames[0][2], `"id":"B"`) {
		t.Errorf("frame 0 = %v, want id 2 payload B", frames[0])
	}
	if frames[1][0] != "3" || frames[1][1] != "receipt" {
		t.Errorf("frame 1 = %v, want id 3 event receipt", frames[1])
	}
}

// TestSSE_TailOnWakeHint pins the store-tail live path: a fresh
// connection (no cursor) skips history, then a row appended after
// connect + a live wake hint arrives as a frame with the ring seq.
func TestSSE_TailOnWakeHint(t *testing.T) {
	fes := newFakeEventStream()
	fr := &fakeReplayer{}
	fr.append("message", `{"id":"OLD"}`) // pre-connect history: must NOT replay
	srv := newReplaySSEServerForTest(t, fes, fr, "secret")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": connected") {
			break
		}
	}

	go func() {
		<-time.After(50 * time.Millisecond)
		fr.append("message", `{"id":"NEW"}`)
		fes.Publish("message", map[string]any{"hint": true})
	}()

	frames := readFrames(t, scanner, 1, 3*time.Second)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1: %v", len(frames), frames)
	}
	if frames[0][0] != "2" || !strings.Contains(frames[0][2], `"id":"NEW"`) {
		t.Errorf("frame = %v, want ring seq 2 payload NEW (history must not replay)", frames[0])
	}
}

// TestSSE_SyntheticDropHasNoID pins FR-063 over SSE: a synthetic
// stream.drop record (Seq 0) is emitted without an id: line so the
// client cursor never regresses.
func TestSSE_SyntheticDropHasNoID(t *testing.T) {
	fes := newFakeEventStream()
	fr := &fakeReplayer{}
	fr.append("message", `{"id":"A"}`)
	fr.append("message", `{"id":"B"}`)
	fr.gap = &ReplayRecord{Kind: "stream.drop", Data: []byte(`{"gap":5}`)}
	srv := newReplaySSEServerForTest(t, fes, fr, "secret")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events?since=0", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	frames := readFrames(t, bufio.NewScanner(resp.Body), 3, 3*time.Second)
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3: %v", len(frames), frames)
	}
	if frames[0][0] != "" || frames[0][1] != "stream.drop" {
		t.Errorf("drop frame = %v, want empty id + event stream.drop", frames[0])
	}
	if frames[1][0] != "1" || frames[2][0] != "2" {
		t.Errorf("real frames = %v %v, want ids 1 and 2", frames[1], frames[2])
	}
}

// TestSSE_BadCursorRejected pins the fail-loud contract: a non-numeric
// Last-Event-ID is a clean 400 BEFORE any SSE bytes.
func TestSSE_BadCursorRejected(t *testing.T) {
	fes := newFakeEventStream()
	fr := &fakeReplayer{}
	srv := newReplaySSEServerForTest(t, fes, fr, "secret")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+srv.ListenerAddr().String()+"/v1/events", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Last-Event-ID", "not-a-number")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
