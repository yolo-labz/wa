package socket

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestStreamDropFrameShape(t *testing.T) {
	raw := streamDropFrame("sub-1", 10, 13)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		t.Fatalf("error not a map: %T", parsed["error"])
	}
	if code, _ := errObj["code"].(float64); int(code) != int(CodeStreamDrop) {
		t.Fatalf("code=%v want %v", code, CodeStreamDrop)
	}
	data, _ := errObj["data"].(map[string]any)
	if data["count"].(float64) != 4 {
		t.Fatalf("count=%v want 4", data["count"])
	}
	if data["oldest_dropped"].(float64) != 10 {
		t.Fatalf("oldest_dropped=%v want 10", data["oldest_dropped"])
	}
	if data["newest_dropped"].(float64) != 13 {
		t.Fatalf("newest_dropped=%v want 13", data["newest_dropped"])
	}
}

func TestSubscribeClosedPongTimeoutFrameShape(t *testing.T) {
	raw := subscribeClosedPongTimeoutFrame("sub-2", 42)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errObj := parsed["error"].(map[string]any)
	if int(errObj["code"].(float64)) != int(CodePongTimeout) {
		t.Fatalf("code=%v", errObj["code"])
	}
	data := errObj["data"].(map[string]any)
	if data["reason"].(string) != "pong_timeout" {
		t.Fatalf("reason=%v", data["reason"])
	}
	if data["resumeSince"].(float64) != 42 {
		t.Fatalf("resumeSince=%v want 42", data["resumeSince"])
	}
}

func TestMatchesSubFilterDSL(t *testing.T) {
	sub := &Subscription{
		events:     map[string]struct{}{"message": {}},
		chats:      []string{"1@c.us", "2@c.us"},
		senders:    []string{"a@c.us"},
		notSenders: []string{"b@c.us"},
	}

	if !matchesSub(sub, Event{Type: "message", Chat: "1@c.us", Sender: "a@c.us"}, nil) {
		t.Fatalf("should match")
	}
	if matchesSub(sub, Event{Type: "message", Chat: "3@c.us", Sender: "a@c.us"}, nil) {
		t.Fatalf("chat outside list should not match")
	}
	if matchesSub(sub, Event{Type: "message", Chat: "1@c.us", Sender: "b@c.us"}, nil) {
		t.Fatalf("notSender should veto")
	}
	if matchesSub(sub, Event{Type: "receipt", Chat: "1@c.us", Sender: "a@c.us"}, nil) {
		t.Fatalf("event type outside set should not match")
	}
}

func TestMatchesSubSinceCursor(t *testing.T) {
	sub := &Subscription{since: 100}
	if matchesSub(sub, Event{Type: "x", Seq: 100}, nil) {
		t.Fatalf("seq==since must not match")
	}
	if matchesSub(sub, Event{Type: "x", Seq: 50}, nil) {
		t.Fatalf("seq<since must not match")
	}
	if !matchesSub(sub, Event{Type: "x", Seq: 101}, nil) {
		t.Fatalf("seq>since must match")
	}
}

func TestTickHeartbeatReapsOverdueSub(t *testing.T) {
	s := NewServer(nil, slog.New(slog.DiscardHandler), WithHeartbeat(10*time.Millisecond, 20*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &Connection{
		subscriptions: make(map[string]*Subscription),
		out:           make(chan []byte, 8),
		ctx:           ctx,
		cancel:        cancel,
		log:           slog.New(slog.DiscardHandler),
	}
	// Overdue subscription: lastPongAt 1h ago.
	conn.subscriptions["reap"] = &Subscription{
		id:         "reap",
		lastPongAt: time.Now().Add(-time.Hour),
		lastSeq:    7,
	}
	s.conns[conn.id] = conn

	s.tickHeartbeat(time.Now())

	if _, still := conn.subscriptions["reap"]; still {
		t.Fatalf("subscription should be reaped")
	}
	// Drain the outbound frame and check it is the pong-timeout closure.
	select {
	case frame := <-conn.out:
		var parsed map[string]any
		_ = json.Unmarshal(frame, &parsed)
		errObj, _ := parsed["error"].(map[string]any)
		data, _ := errObj["data"].(map[string]any)
		if data["reason"].(string) != "pong_timeout" {
			t.Fatalf("expected pong_timeout frame, got %v", parsed)
		}
		if data["resumeSince"].(float64) != 7 {
			t.Fatalf("resumeSince=%v want 7", data["resumeSince"])
		}
	default:
		t.Fatalf("expected pong_timeout frame on out chan")
	}
}
