package sockettest

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// rpcNotification is a JSON-RPC 2.0 server notification (no id).
type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *rpcResponseErr `json:"error,omitempty"`
}

// notificationParams holds the common fields of an event notification.
type notificationParams struct {
	Schema         string `json:"schema"`
	Type           string `json:"type"`
	SubscriptionID string `json:"subscriptionId"`
}

// subscribe sends a subscribe request and returns the subscriptionId.
func subscribe(t *testing.T, conn net.Conn, scanner *bufio.Scanner, events []string) string {
	t.Helper()
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	sendLine(t, conn, `{"jsonrpc":"2.0","id":1,"method":"subscribe","params":{"events":`+string(eventsJSON)+`}}`)
	resp := recvResponse(t, scanner)
	if resp.Error != nil {
		t.Fatalf("subscribe failed: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}
	var result struct {
		SubscriptionID string `json:"subscriptionId"`
		Schema         string `json:"schema"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal subscribe result: %v", err)
	}
	if result.SubscriptionID == "" {
		t.Fatal("subscribe returned empty subscriptionId")
	}
	if result.Schema != "wa.event/v1" {
		t.Errorf("subscribe schema = %q, want wa.event/v1", result.Schema)
	}
	return result.SubscriptionID
}

// recvNotification reads one JSON-RPC notification from the scanner.
func recvNotification(t *testing.T, scanner *bufio.Scanner) rpcNotification {
	t.Helper()
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("recvNotification scan: %v", err)
		}
		t.Fatal("recvNotification: connection closed before receiving a line")
	}
	var notif rpcNotification
	if err := json.Unmarshal(scanner.Bytes(), &notif); err != nil {
		t.Fatalf("recvNotification unmarshal %q: %v", scanner.Text(), err)
	}
	return notif
}

// recvNotificationWithTimeout reads one notification with a timeout. Returns
// (notification, true) on success or (zero, false) on timeout.
func recvNotificationWithTimeout(t *testing.T, conn net.Conn, scanner *bufio.Scanner, timeout time.Duration) (rpcNotification, bool) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	if !scanner.Scan() {
		return rpcNotification{}, false
	}
	var notif rpcNotification
	if err := json.Unmarshal(scanner.Bytes(), &notif); err != nil {
		t.Fatalf("recvNotificationWithTimeout unmarshal: %v", err)
	}
	return notif, true
}

// T044: subscribe returns subscriptionId + schema, then receives matching events.
func TestSubscribe_ReceiveMatchingEvents(t *testing.T) {
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	subID := subscribe(t, conn, scanner, []string{"message"})

	// Push a matching event.
	fake.PushEvent(socket.Event{Type: "message"})

	notif := recvNotification(t, scanner)
	if notif.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", notif.JSONRPC)
	}
	if notif.Method != "event" {
		t.Errorf("method = %q, want event", notif.Method)
	}

	var params notificationParams
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.Type != "message" {
		t.Errorf("params.type = %q, want message", params.Type)
	}
	if params.Schema != "wa.event/v1" {
		t.Errorf("params.schema = %q, want wa.event/v1", params.Schema)
	}
	if params.SubscriptionID != subID {
		t.Errorf("params.subscriptionId = %q, want %q", params.SubscriptionID, subID)
	}
}

// T045: events not in filter are not delivered.
func TestSubscribe_FilteredEventsNotDelivered(t *testing.T) {
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	_ = subscribe(t, conn, scanner, []string{"message"})

	// Push a non-matching event.
	fake.PushEvent(socket.Event{Type: "receipt"})

	// Should NOT receive anything within a short window.
	notif, ok := recvNotificationWithTimeout(t, conn, scanner, 200*time.Millisecond)
	if ok {
		t.Fatalf("received unexpected notification: method=%q", notif.Method)
	}

	// Now push a matching event — it SHOULD arrive.
	fake.PushEvent(socket.Event{Type: "message"})

	notif2 := recvNotification(t, scanner)
	if notif2.Method != "event" {
		t.Errorf("method = %q, want event", notif2.Method)
	}
	var params notificationParams
	if err := json.Unmarshal(notif2.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.Type != "message" {
		t.Errorf("params.type = %q, want message", params.Type)
	}
}

// T046: notification carries schema and subscriptionId.
func TestSubscribe_NotificationCarriesSchemaAndSubID(t *testing.T) {
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	subID := subscribe(t, conn, scanner, []string{"receipt"})

	fake.PushEvent(socket.Event{Type: "receipt"})

	notif := recvNotification(t, scanner)

	// Verify exact fields.
	if notif.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", notif.JSONRPC)
	}
	if notif.Method != "event" {
		t.Errorf("method = %q, want event", notif.Method)
	}

	var params notificationParams
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.Schema != "wa.event/v1" {
		t.Errorf("params.schema = %q, want wa.event/v1", params.Schema)
	}
	if params.SubscriptionID != subID {
		t.Errorf("params.subscriptionId = %q, want %q", params.SubscriptionID, subID)
	}
	if params.Type != "receipt" {
		t.Errorf("params.type = %q, want receipt", params.Type)
	}
}

// T047: backpressure close after buffer fills.
func TestSubscribe_BackpressureClose(t *testing.T) {
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	_ = subscribe(t, conn, scanner, []string{"message"})

	// Push 1025 events rapidly without reading.
	// The outbound mailbox has capacity 1024; the 1025th triggers backpressure.
	for i := 0; i < 1025; i++ {
		fake.PushEvent(socket.Event{Type: "message"})
	}

	// Give the fan-out goroutine time to process.
	time.Sleep(500 * time.Millisecond)

	// The connection should be closed (or closing). Try to read — we should
	// eventually hit EOF or get the backpressure error frame.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	// Drain all available lines. We expect to eventually see either a
	// backpressure error or EOF.
	foundBackpressure := false
	for scanner.Scan() {
		var msg rpcNotification
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Error != nil && msg.Error.Code == int(socket.CodeBackpressure) {
			foundBackpressure = true
			break
		}
	}

	// Either we found the backpressure frame or the connection was closed.
	// Both are acceptable — the key invariant is that the connection was closed.
	if !foundBackpressure {
		// Verify the connection is actually closed by trying to write.
		_, err := conn.Write([]byte("test\n"))
		if err == nil {
			t.Error("expected connection to be closed after backpressure, but write succeeded")
		}
	}
}

// T048: unsubscribe stops delivery.
func TestSubscribe_UnsubscribeStopsDelivery(t *testing.T) {
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	subID := subscribe(t, conn, scanner, []string{"message"})

	// Unsubscribe.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":2,"method":"unsubscribe","params":{"subscriptionId":"`+subID+`"}}`)
	resp := recvResponse(t, scanner)
	if resp.Error != nil {
		t.Fatalf("unsubscribe failed: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}

	// Push an event — should NOT be delivered.
	fake.PushEvent(socket.Event{Type: "message"})

	notif, ok := recvNotificationWithTimeout(t, conn, scanner, 200*time.Millisecond)
	if ok {
		t.Fatalf("received notification after unsubscribe: method=%q", notif.Method)
	}
}

// T049: connection close releases subscriptions, no goroutine leak.
func TestSubscribe_ConnectionCloseReleasesSubscriptions(t *testing.T) {
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	_ = subscribe(t, conn, scanner, []string{"message"})

	// Close the client connection.
	conn.Close()

	// Give the server time to clean up.
	time.Sleep(200 * time.Millisecond)

	// Push an event — should not panic or block, since the connection is gone.
	// This validates the server cleaned up the subscription.
	fake.PushEvent(socket.Event{Type: "message"})

	// If we get here without panic or hang, the test passes.
	// goleak in TestMain catches leaked goroutines.
}

// T050: dispatcher closing Events channel triggers SubscriptionClosed.
func TestSubscribe_DispatcherCloseTriggersSubscriptionClosed(t *testing.T) {
	// We need a fresh FakeDispatcher that we can close independently.
	// The startServer helper closes the FakeDispatcher in cleanup, so we
	// need to close it ourselves first.
	fake, path := startServer(t, nil)
	conn, scanner := dial(t, path)

	_ = subscribe(t, conn, scanner, []string{"message"})

	// Close the dispatcher's event channel.
	fake.Close()

	// We should receive a -32005 SubscriptionClosed error frame.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	// Read until we find the subscription closed frame or EOF.
	found := false
	for scanner.Scan() {
		var msg rpcNotification
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Error != nil && msg.Error.Code == int(socket.CodeSubscriptionClosed) {
			found = true
			break
		}
	}
	if !found {
		t.Error("did not receive SubscriptionClosed (-32005) notification after dispatcher close")
	}
}
