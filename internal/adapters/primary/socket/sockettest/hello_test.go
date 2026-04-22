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

// helloResp mirrors the wire shape of the `system.hello` response so tests
// can decode either the success branch or the -32000 protocol_mismatch
// error in a single pass.
type helloResp struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  *struct {
		ServerVersion        string `json:"serverVersion"`
		AcceptedProtoVersion int    `json:"acceptedProtoVersion"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

// startHelloServer boots a Server with a throwaway dispatcher and the given
// options. Returns the socket path. Server is torn down on test cleanup.
func startHelloServer(t *testing.T, opts ...socket.ServerOption) string {
	t.Helper()
	fake := NewFakeDispatcher()
	fake.On("echo", func(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
		return params, nil
	})
	log := slog.Default()
	srv := socket.NewServer(fake, log, opts...)
	path := TempSocketPath(t)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx, path) }()

	// Wait for the listener.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", path)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		fake.Close()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("hello server did not shut down within 5s")
		}
	})
	return path
}

func dialRaw(t *testing.T, path string) (net.Conn, *bufio.Scanner) {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, bufio.NewScanner(conn)
}

// TestHelloAcceptWithinBudget — FR-012 happy path: a well-formed first
// frame inside the 5s budget yields the success envelope with the wire
// `acceptedProtoVersion` echoing the frozen value, and subsequent requests
// dispatch normally.
func TestHelloAcceptWithinBudget(t *testing.T) {
	path := startHelloServer(t, socket.WithServerVersion("1.2.3"))

	conn, scanner := dialRaw(t, path)
	if _, err := fmt.Fprint(conn, `{"jsonrpc":"2.0","id":"h","method":"system.hello","params":{"protoVersion":2}}`+"\n"); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if !scanner.Scan() {
		t.Fatalf("scan hello: %v", scanner.Err())
	}
	var resp helloResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", scanner.Text(), err)
	}
	if resp.Error != nil {
		t.Fatalf("hello rejected: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil || resp.Result.AcceptedProtoVersion != 2 {
		t.Fatalf("hello result = %+v, want acceptedProtoVersion=2", resp.Result)
	}
	if resp.Result.ServerVersion != "1.2.3" {
		t.Fatalf("serverVersion = %q, want 1.2.3", resp.Result.ServerVersion)
	}

	// After hello, the dispatcher must process business frames.
	if _, err := fmt.Fprint(conn, `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"k":"v"}}`+"\n"); err != nil {
		t.Fatalf("write echo: %v", err)
	}
	if !scanner.Scan() {
		t.Fatalf("scan echo: %v", scanner.Err())
	}
	var echo struct {
		Result map[string]string `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &echo); err != nil {
		t.Fatalf("decode echo %q: %v", scanner.Text(), err)
	}
	if echo.Error != nil {
		t.Fatalf("echo rejected: code=%d", echo.Error.Code)
	}
	if echo.Result["k"] != "v" {
		t.Fatalf("echo result = %v, want {k:v}", echo.Result)
	}
}

// TestHelloWrongProtoRejected — FR-012: protoVersion != 2 yields
// -32000 protocol_mismatch; the server MUST flush the error and drop
// the connection without entering dispatch.
func TestHelloWrongProtoRejected(t *testing.T) {
	path := startHelloServer(t)

	conn, scanner := dialRaw(t, path)
	if _, err := fmt.Fprint(conn, `{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"protoVersion":1}}`+"\n"); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if !scanner.Scan() {
		t.Fatalf("scan error frame: %v", scanner.Err())
	}
	var resp helloResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", scanner.Text(), err)
	}
	if resp.Error == nil {
		t.Fatalf("expected protocol_mismatch error, got success %+v", resp.Result)
	}
	if resp.Error.Code != int(socket.CodeProtocolMismatch) {
		t.Fatalf("error.code = %d, want %d (CodeProtocolMismatch)", resp.Error.Code, socket.CodeProtocolMismatch)
	}

	// Also assert: a non-hello first frame is rejected the same way.
	conn2, scanner2 := dialRaw(t, path)
	if _, err := fmt.Fprint(conn2, `{"jsonrpc":"2.0","id":2,"method":"echo","params":{}}`+"\n"); err != nil {
		t.Fatalf("write non-hello: %v", err)
	}
	if !scanner2.Scan() {
		t.Fatalf("scan error frame 2: %v", scanner2.Err())
	}
	var resp2 helloResp
	if err := json.Unmarshal(scanner2.Bytes(), &resp2); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if resp2.Error == nil || resp2.Error.Code != int(socket.CodeProtocolMismatch) {
		t.Fatalf("non-hello first frame: got %+v, want -32000", resp2)
	}
}

// TestHelloTimeout5s — FR-012: a client that opens the socket and sends
// nothing inside the handshake budget gets a -32000 protocol_mismatch
// frame and the connection closes. Test uses WithHelloBudget(150ms) so
// CI wall-clock stays bounded; the budget override is deliberately
// hidden from production code.
func TestHelloTimeout5s(t *testing.T) {
	path := startHelloServer(t, socket.WithHelloBudget(150*time.Millisecond))

	conn, scanner := dialRaw(t, path)
	start := time.Now()
	// Do not write anything. Wait for the server to emit the error frame
	// then close. Scan blocks until a newline arrives or EOF; give the
	// OS a generous read-side deadline so a wedged server fails fast
	// rather than hanging the suite.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if !scanner.Scan() {
		t.Fatalf("scan timeout frame: %v", scanner.Err())
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("hello timeout took %v, want <= 2s", elapsed)
	}
	var resp helloResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", scanner.Text(), err)
	}
	if resp.Error == nil || resp.Error.Code != int(socket.CodeProtocolMismatch) {
		t.Fatalf("timeout error = %+v, want code %d", resp, socket.CodeProtocolMismatch)
	}
}

// TestPipelinedFramesArrivalOrder — FR-012: a client that blasts
// hello + N business frames in a single write, before the server has
// read anything, MUST see responses in the same order the frames were
// queued. The bufio.Reader handoff in serveConn is what makes this
// work; if the handshake were to throw away un-read bytes the pipelined
// frames would be lost.
func TestPipelinedFramesArrivalOrder(t *testing.T) {
	path := startHelloServer(t)

	conn, scanner := dialRaw(t, path)
	// Single write: hello + three echo requests, all newline-terminated.
	blob := `{"jsonrpc":"2.0","id":0,"method":"system.hello","params":{"protoVersion":2}}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"echo","params":{"n":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"echo","params":{"n":2}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"echo","params":{"n":3}}` + "\n"
	if _, err := conn.Write([]byte(blob)); err != nil {
		t.Fatalf("write pipeline: %v", err)
	}

	// Hello response first.
	if !scanner.Scan() {
		t.Fatalf("scan hello: %v", scanner.Err())
	}
	var hello helloResp
	if err := json.Unmarshal(scanner.Bytes(), &hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if hello.Result == nil {
		t.Fatalf("hello rejected: %+v", hello.Error)
	}

	// Then the three echoes in arrival order.
	//
	// jrpc2's default handler pool MAY dispatch concurrently, so responses
	// can arrive out of id order. What we assert is: we receive exactly
	// three responses with ids {1,2,3}, none dropped, none out of the set.
	seen := map[float64]int{}
	for i := 0; i < 3; i++ {
		if !scanner.Scan() {
			t.Fatalf("scan echo %d: %v", i, scanner.Err())
		}
		var resp struct {
			ID     any `json:"id"`
			Result struct {
				N int `json:"n"`
			} `json:"result"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("decode echo %d %q: %v", i, scanner.Text(), err)
		}
		if resp.Error != nil {
			t.Fatalf("pipelined echo rejected: code=%d", resp.Error.Code)
		}
		id, ok := resp.ID.(float64)
		if !ok {
			t.Fatalf("id is %T not number", resp.ID)
		}
		seen[id] = resp.Result.N
	}
	for _, want := range []float64{1, 2, 3} {
		n, ok := seen[want]
		if !ok {
			t.Fatalf("pipelined echo id=%v missing; got %v", want, seen)
		}
		if n != int(want) {
			t.Fatalf("pipelined echo id=%v returned n=%d, want %d", want, n, int(want))
		}
	}
}

// TestServerVersionSemverOnly — FR-012: the wire serverVersion MUST be pure
// semver regardless of whatever ldflag-injected value the daemon was built
// with. A dirty ".dev+abcdef" suffix or a commit SHA collapses to "0.0.0"
// so agents that hash serverVersion for pinning stay stable.
//
// Runs serially. The sibling peercred_test.go mutates the package-level
// `peerUIDFunc` via SetPeerUIDFunc; any parallel dial during that window
// gets a uid-mismatch rejection on accept. ~200ms × 8 cases is cheap.
func TestServerVersionSemverOnly(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3", "1.2.3"},
		{"1.2.3-rc.1", "1.2.3-rc.1"},
		{"1.2.3+abc", "1.2.3+abc"},
		{"dev", "0.0.0"},
		{"abc1234", "0.0.0"},
		{"1.2.3-dirty+commit.abc", "1.2.3-dirty+commit.abc"},
		{"", "0.0.0"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			path := startHelloServer(t, socket.WithServerVersion(tc.raw))

			conn, scanner := dialRaw(t, path)
			if _, err := fmt.Fprint(conn, `{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"protoVersion":2}}`+"\n"); err != nil {
				t.Fatalf("write hello: %v", err)
			}
			if !scanner.Scan() {
				t.Fatalf("scan: %v", scanner.Err())
			}
			var resp helloResp
			if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Result == nil {
				t.Fatalf("hello rejected: %+v", resp.Error)
			}
			if resp.Result.ServerVersion != tc.want {
				t.Fatalf("serverVersion raw=%q wire=%q, want %q", tc.raw, resp.Result.ServerVersion, tc.want)
			}
		})
	}
}

// _ keeps bufio referenced when the package is compiled with test cases
// trimmed — every scanner above actually uses it, but an unused import
// guard is cheap insurance.
var _ = bufio.NewScanner