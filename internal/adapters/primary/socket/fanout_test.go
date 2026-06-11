package socket

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// testUnixPair returns both ends of a connected unix-socket pair. The
// listener side is what newConnection expects; the client side lets a
// test read (or deliberately not read) what the connection writes.
func testUnixPair(t *testing.T) (server, client *net.UnixConn) {
	t.Helper()
	addr := &net.UnixAddr{Name: filepath.Join(t.TempDir(), "t.sock"), Net: "unix"}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()

	type accepted struct {
		conn *net.UnixConn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, aerr := l.AcceptUnix()
		ch <- accepted{c, aerr}
	}()
	client, err = net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	a := <-ch
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	t.Cleanup(func() { _ = a.conn.Close(); _ = client.Close() })
	return a.conn, client
}

func testConn(t *testing.T) *Connection {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Connection{
		subscriptions: make(map[string]*Subscription),
		out:           make(chan []byte, 8),
		ctx:           ctx,
		cancel:        cancel,
		log:           slog.New(slog.DiscardHandler),
	}
}

// TestFanOutEventAdvancesLastSeq pins the post-CON-03 restructure:
// frames are pushed outside c.mu, and lastSeq still advances on a
// successful push.
func TestFanOutEventAdvancesLastSeq(t *testing.T) {
	s := NewServer(nil, slog.New(slog.DiscardHandler))
	conn := testConn(t)
	sub := &Subscription{id: "s1", lastSeq: 4}
	conn.subscriptions["s1"] = sub
	s.conns[conn.id] = conn

	s.fanOutEvent(Event{Type: "message", Seq: 5})

	select {
	case frame := <-conn.out:
		var parsed map[string]any
		_ = json.Unmarshal(frame, &parsed)
		if parsed["method"] == nil && parsed["error"] != nil {
			t.Fatalf("expected event notification, got error frame: %v", parsed)
		}
	default:
		t.Fatal("expected one event frame on out chan")
	}
	conn.mu.Lock()
	got := sub.lastSeq
	conn.mu.Unlock()
	if got != 5 {
		t.Fatalf("lastSeq=%d want 5", got)
	}
}

// TestFanOutEventEmitsDropFrameOnGap: ring-buffer gap between lastSeq
// and the event seq must yield a stream.drop frame BEFORE the event
// frame (FR-063) — ordering preserved across the CON-03 restructure.
func TestFanOutEventEmitsDropFrameOnGap(t *testing.T) {
	s := NewServer(nil, slog.New(slog.DiscardHandler))
	conn := testConn(t)
	sub := &Subscription{id: "s1", lastSeq: 2}
	conn.subscriptions["s1"] = sub
	s.conns[conn.id] = conn

	s.fanOutEvent(Event{Type: "message", Seq: 5})

	frame1 := <-conn.out
	var drop map[string]any
	_ = json.Unmarshal(frame1, &drop)
	errObj, _ := drop["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("first frame must be the stream.drop error frame, got %v", drop)
	}
	if code, _ := errObj["code"].(float64); int(code) != int(CodeStreamDrop) {
		t.Fatalf("first frame code=%v want %v", code, CodeStreamDrop)
	}
	select {
	case <-conn.out: // the event frame
	default:
		t.Fatal("expected event frame after drop frame")
	}
	conn.mu.Lock()
	got := sub.lastSeq
	conn.mu.Unlock()
	if got != 5 {
		t.Fatalf("lastSeq=%d want 5", got)
	}
}

// TestFanOutEventBackpressureSkipsLastSeq: when the push fails on
// backpressure the subscription cursor must NOT advance — the client
// resumes from the last frame actually delivered.
func TestFanOutEventBackpressureSkipsLastSeq(t *testing.T) {
	raw, _ := testUnixPair(t)
	s := NewServer(nil, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn := &Connection{
		raw:           raw,
		subscriptions: make(map[string]*Subscription),
		out:           make(chan []byte), // unbuffered + no writer = instant backpressure
		ctx:           ctx,
		cancel:        cancel,
		log:           slog.New(slog.DiscardHandler),
	}
	sub := &Subscription{id: "s1", lastSeq: 4}
	conn.subscriptions["s1"] = sub
	s.conns[conn.id] = conn

	s.fanOutEvent(Event{Type: "message", Seq: 5})

	conn.mu.Lock()
	got := sub.lastSeq
	conn.mu.Unlock()
	if got != 4 {
		t.Fatalf("lastSeq=%d want 4 (no advance on failed push)", got)
	}
	select {
	case <-conn.ctx.Done(): // backpressure cancels the connection
	case <-time.After(5 * time.Second):
		t.Fatal("backpressure did not cancel the connection")
	}
}

// TestSendShutdownNotificationsPerSub: one -32002 frame per active
// subscription, pushed outside c.mu (CON-03 restructure regression pin).
func TestSendShutdownNotificationsPerSub(t *testing.T) {
	s := NewServer(nil, slog.New(slog.DiscardHandler))
	conn := testConn(t)
	conn.subscriptions["a"] = &Subscription{id: "a"}
	conn.subscriptions["b"] = &Subscription{id: "b"}
	s.conns[conn.id] = conn

	s.sendShutdownNotifications()

	for i := range 2 {
		select {
		case frame := <-conn.out:
			var parsed map[string]any
			_ = json.Unmarshal(frame, &parsed)
			errObj, _ := parsed["error"].(map[string]any)
			if code, _ := errObj["code"].(float64); int(code) != int(CodeShutdownInProgress) {
				t.Fatalf("frame %d code=%v want %v", i, code, CodeShutdownInProgress)
			}
		default:
			t.Fatalf("expected 2 shutdown frames, got %d", i)
		}
	}
}

// TestWriterExitCancelsConnCtx pins CON-09: the writer goroutine must
// cancel the connection ctx on EVERY exit path — including a plain
// channel close — so the conn's derived resources never outlive it.
func TestWriterExitCancelsConnCtx(t *testing.T) {
	raw, client := testUnixPair(t)
	c := newConnection(1, 0, raw, context.Background(), slog.New(slog.DiscardHandler))
	c.startWriter()
	_ = client // reader side not needed; writer exits via channel close

	close(c.out)
	select {
	case <-c.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("conn ctx not cancelled after writer exit via channel close")
	}
}
