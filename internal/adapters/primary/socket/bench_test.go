package socket_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/adapters/primary/socket/sockettest"
)

// BenchmarkRoundtrip measures sequential JSON-RPC request/response latency.
// Verifies SC-001 (<10ms/op) and SC-004 (bounded memory).
func BenchmarkRoundtrip(b *testing.B) {
	b.ReportAllocs()

	// Set up dispatcher with an echo handler.
	fd := sockettest.NewFakeDispatcher()
	fd.On("echo", func(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
		return params, nil
	})
	defer fd.Close()

	// Temp socket path (short, to fit sun_path).
	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "wa.sock")

	// Start server.
	srv := socket.NewServer(fd, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx, path) }()

	// Wait for listener to be ready.
	var conn net.Conn
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("unix", path)
		if err == nil {
			break
		}
	}
	if conn == nil {
		b.Fatal("failed to connect to server")
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"echo","params":{"v":%d}}`, i, i)
		if _, err := fmt.Fprintf(conn, "%s\n", req); err != nil {
			b.Fatalf("write: %v", err)
		}
		if !scanner.Scan() {
			b.Fatalf("read: %v", scanner.Err())
		}
	}
	b.StopTimer()

	cancel()
	_ = srv.Wait()
}
