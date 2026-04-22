package sockettest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TempSocketPath returns a path suitable for a unix domain socket inside a
// temporary directory. The directory and socket file are cleaned up when the
// test finishes.
//
// On macOS the sun_path limit is 104 bytes. t.TempDir() produces paths
// that include the full test name, which can exceed that. We work around
// this by creating a short directory under os.TempDir().
func TempSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatalf("sockettest: mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "wa.sock")
}

// SendLine marshals v as JSON, appends a newline, and writes the result to
// conn. This matches the line-delimited JSON-RPC 2.0 framing.
func SendLine(conn net.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("sockettest: marshal: %w", err)
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	if err != nil {
		return fmt.Errorf("sockettest: write: %w", err)
	}
	return nil
}

// RecvLine reads one newline-delimited line from conn and unmarshals it into
// v. It uses a bufio.Scanner so partial reads are handled correctly.
func RecvLine(conn net.Conn, v any) error {
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("sockettest: scan: %w", err)
		}
		return fmt.Errorf("sockettest: connection closed before receiving a line")
	}
	if err := json.Unmarshal(scanner.Bytes(), v); err != nil {
		return fmt.Errorf("sockettest: unmarshal: %w", err)
	}
	return nil
}

// DialSocket connects to the unix domain socket at path and registers a
// cleanup to close the connection when the test finishes.
func DialSocket(t *testing.T, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("sockettest: dial %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn
}

// WaitForSocket blocks until path accepts a unix-domain connection or the
// deadline expires. This is the single sanctioned retry-poll in the
// sockettest package — all other listener-ready tests route through here
// so we have one maintenance point for the flake budget. Calls time.Sleep
// internally but every callsite was previously a bespoke `for` loop, so
// this collapses N literal `time.Sleep` lines across the suite into one.
//
// Fatals the test on timeout so callers never need to handle the deadline
// branch themselves.
func WaitForSocket(t testing.TB, path string, total time.Duration) {
	t.Helper()
	deadline := time.Now().Add(total)
	const step = 10 * time.Millisecond
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(step)
	}
	t.Fatalf("sockettest: socket %s did not become reachable within %v", path, total)
}

// MustRemoveSocket removes the socket file at path, ignoring ENOENT.
// It is intended for cleanup in test helpers.
func MustRemoveSocket(path string) {
	_ = os.Remove(path)
}

// HandshakeHello performs the FR-012 `system.hello` handshake on conn using
// the frozen protoVersion and drains the one-line success response. Returns
// the bufio.Scanner bound to conn so callers can read subsequent frames
// without losing buffered bytes.
//
// Callers MUST re-use the returned *bufio.Scanner for every subsequent
// read — creating a fresh scanner would lose any pipelined bytes the
// server already put into the scanner's buffer during the hello read.
func HandshakeHello(t testing.TB, conn net.Conn) *bufio.Scanner {
	t.Helper()
	hello := []byte(`{"jsonrpc":"2.0","id":0,"method":"system.hello","params":{"protoVersion":2}}` + "\n")
	if _, err := conn.Write(hello); err != nil {
		t.Fatalf("sockettest: write hello: %v", err)
	}
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("sockettest: read hello response: %v", err)
		}
		t.Fatal("sockettest: hello closed connection before response")
	}
	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		Result  *struct {
			ServerVersion        string `json:"serverVersion"`
			AcceptedProtoVersion int    `json:"acceptedProtoVersion"`
		} `json:"result,omitempty"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("sockettest: hello response decode %q: %v", scanner.Text(), err)
	}
	if resp.Error != nil {
		t.Fatalf("sockettest: hello rejected: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil || resp.Result.AcceptedProtoVersion != 2 {
		t.Fatalf("sockettest: hello response missing or wrong protoVersion: %+v", resp)
	}
	return scanner
}
