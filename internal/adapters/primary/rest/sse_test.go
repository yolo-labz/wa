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
