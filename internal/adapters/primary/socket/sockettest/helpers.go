package sockettest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
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

// MustRemoveSocket removes the socket file at path, ignoring ENOENT.
// It is intended for cleanup in test helpers.
func MustRemoveSocket(path string) {
	_ = os.Remove(path)
}
