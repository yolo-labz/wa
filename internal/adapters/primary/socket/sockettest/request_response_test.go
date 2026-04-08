package sockettest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// rpcResponse is a generic JSON-RPC 2.0 response for test assertions.
type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      any              `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcResponseErr  `json:"error,omitempty"`
}

type rpcResponseErr struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Data    json.RawMessage  `json:"data,omitempty"`
}

// sendLine writes a raw string line to the connection.
func sendLine(t *testing.T, conn net.Conn, line string) {
	t.Helper()
	_, err := fmt.Fprintf(conn, "%s\n", line)
	if err != nil {
		t.Fatalf("sendLine: %v", err)
	}
}

// recvResponse reads one JSON-RPC response from the scanner.
func recvResponse(t *testing.T, scanner *bufio.Scanner) rpcResponse {
	t.Helper()
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("recvResponse scan: %v", err)
		}
		t.Fatal("recvResponse: connection closed before receiving a line")
	}
	var resp rpcResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("recvResponse unmarshal %q: %v", scanner.Text(), err)
	}
	return resp
}

// startServer creates a FakeDispatcher, a Server, starts it on a temp socket,
// and returns the dispatcher, the socket path, and a cleanup function.
func startServer(t *testing.T, setup func(d *FakeDispatcher)) (*FakeDispatcher, string) {
	t.Helper()
	fake := NewFakeDispatcher()
	if setup != nil {
		setup(fake)
	}

	log := slog.Default()
	srv := socket.NewServer(fake, log)
	path := TempSocketPath(t)

	ctx, cancel := context.WithCancel(context.Background())

	// Channel to capture Run errors.
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx, path)
	}()

	// Wait for the socket to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		fake.Close()
		select {
		case err := <-errCh:
			if err != nil {
				t.Logf("server.Run returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not shut down within 5s")
		}
	})

	return fake, path
}

// dial connects to the socket and returns the conn and a reusable scanner.
func dial(t *testing.T, path string) (net.Conn, *bufio.Scanner) {
	t.Helper()
	conn := DialSocket(t, path)
	scanner := bufio.NewScanner(conn)
	return conn, scanner
}

// T024: echo request roundtrip.
func TestRequestResponse_EchoRoundtrip(t *testing.T) {
	_, path := startServer(t, func(d *FakeDispatcher) {
		d.On("echo", func(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
			return params, nil
		})
	})

	conn, scanner := dial(t, path)

	sendLine(t, conn, `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"hello":"world"}}`)
	resp := recvResponse(t, scanner)

	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", resp.JSONRPC)
	}
	// ID should be 1 (number).
	idFloat, ok := resp.ID.(float64)
	if !ok || idFloat != 1 {
		t.Errorf("id = %v (%T), want 1", resp.ID, resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}

	var result map[string]string
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["hello"] != "world" {
		t.Errorf("result = %v, want {hello:world}", result)
	}
}

// T025: method not found returns -32601.
func TestRequestResponse_MethodNotFound(t *testing.T) {
	_, path := startServer(t, nil) // no handlers registered

	conn, scanner := dial(t, path)

	sendLine(t, conn, `{"jsonrpc":"2.0","id":2,"method":"nope","params":{}}`)
	resp := recvResponse(t, scanner)

	if resp.Error == nil {
		t.Fatal("expected error, got success")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", resp.Error.Code)
	}
}

// T026: parse error returns -32700 and connection stays open.
func TestRequestResponse_ParseError(t *testing.T) {
	_, path := startServer(t, func(d *FakeDispatcher) {
		d.On("echo", func(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
			return params, nil
		})
	})

	conn, scanner := dial(t, path)

	// Send garbage.
	sendLine(t, conn, "not json at all")
	resp := recvResponse(t, scanner)

	if resp.Error == nil {
		t.Fatal("expected error for garbage input")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("error.code = %d, want -32700", resp.Error.Code)
	}

	// Now send a valid request — connection should still be alive.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":3,"method":"echo","params":{"alive":"yes"}}`)
	resp2 := recvResponse(t, scanner)

	if resp2.Error != nil {
		t.Fatalf("expected success after parse error, got error: code=%d message=%q",
			resp2.Error.Code, resp2.Error.Message)
	}

	var result map[string]string
	if err := json.Unmarshal(resp2.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["alive"] != "yes" {
		t.Errorf("result = %v, want {alive:yes}", result)
	}
}

// T027: invalid envelope returns -32600.
func TestRequestResponse_InvalidEnvelope(t *testing.T) {
	_, path := startServer(t, nil)

	conn, scanner := dial(t, path)

	// Wrong version field.
	sendLine(t, conn, `{"jsonrpc":"1.0","id":1,"method":"x"}`)
	resp := recvResponse(t, scanner)

	if resp.Error == nil {
		t.Fatal("expected error for wrong jsonrpc version")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("error.code = %d, want -32600", resp.Error.Code)
	}

	// Missing method field.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":2}`)
	resp2 := recvResponse(t, scanner)

	if resp2.Error == nil {
		t.Fatal("expected error for missing method")
	}
	if resp2.Error.Code != -32600 {
		t.Errorf("error.code = %d, want -32600 for missing method", resp2.Error.Code)
	}
}

// T028: typed error mapping — ErrBackpressure maps to -32001.
func TestRequestResponse_TypedErrorMapping(t *testing.T) {
	_, path := startServer(t, func(d *FakeDispatcher) {
		d.On("backpressure", func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return nil, socket.ErrBackpressure
		})
		d.On("shutdown", func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return nil, socket.ErrShutdown
		})
	})

	conn, scanner := dial(t, path)

	// Test ErrBackpressure → -32001.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":10,"method":"backpressure","params":{}}`)
	resp := recvResponse(t, scanner)

	if resp.Error == nil {
		t.Fatal("expected error for backpressure")
	}
	if resp.Error.Code != int(socket.CodeBackpressure) {
		t.Errorf("error.code = %d, want %d (Backpressure)", resp.Error.Code, socket.CodeBackpressure)
	}

	// Test ErrShutdown → -32002.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":11,"method":"shutdown","params":{}}`)
	resp2 := recvResponse(t, scanner)

	if resp2.Error == nil {
		t.Fatal("expected error for shutdown")
	}
	if resp2.Error.Code != int(socket.CodeShutdownInProgress) {
		t.Errorf("error.code = %d, want %d (ShutdownInProgress)", resp2.Error.Code, socket.CodeShutdownInProgress)
	}
}

// T029: panic recovery — dispatcher panic returns -32603 and server stays alive.
func TestRequestResponse_PanicRecovery(t *testing.T) {
	_, path := startServer(t, func(d *FakeDispatcher) {
		d.On("boom", func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			panic("test panic in dispatcher")
		})
		d.On("echo", func(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
			return params, nil
		})
	})

	conn, scanner := dial(t, path)

	// Trigger the panic.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":20,"method":"boom","params":{}}`)
	resp := recvResponse(t, scanner)

	if resp.Error == nil {
		t.Fatal("expected error for panic")
	}
	if resp.Error.Code != -32603 {
		t.Errorf("error.code = %d, want -32603 (Internal error)", resp.Error.Code)
	}

	// Server should still be alive — send another request.
	sendLine(t, conn, `{"jsonrpc":"2.0","id":21,"method":"echo","params":{"still":"alive"}}`)
	resp2 := recvResponse(t, scanner)

	if resp2.Error != nil {
		t.Fatalf("expected success after panic, got error: code=%d message=%q",
			resp2.Error.Code, resp2.Error.Message)
	}

	var result map[string]string
	if err := json.Unmarshal(resp2.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["still"] != "alive" {
		t.Errorf("result = %v, want {still:alive}", result)
	}
}
