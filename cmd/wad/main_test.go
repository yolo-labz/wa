package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestShutdownClean constructs key wad components in-process using the
// memory adapter (no whatsmeow), starts the socket server, verifies it
// responds to a status request, then cancels the context (simulating
// SIGTERM) and asserts the function returns within 5s. This exercises the
// shutdown path without a real WhatsApp connection.
func TestShutdownClean(t *testing.T) {
	t.Parallel()

	// Use a temp directory for the socket to avoid collisions.
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "wa.sock")

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Build the full wiring with memory adapter.
	mem := memory.New(nil)
	dispatcher := app.NewDispatcher(app.DispatcherConfig{
		Sender:         mem,
		Events:         mem,
		Contacts:       mem,
		Groups:         mem,
		Session:        mem,
		Allowlist:      mem,
		Audit:          mem,
		History:        mem,
		Pairer:         mem,
		SessionCreated: time.Now(),
		Logger:         log,
	})
	t.Cleanup(func() { _ = dispatcher.Close() })

	// Wire the dispatcherAdapter bridge.
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	da := newDispatcherAdapter(bridgeCtx, dispatcher, nil)

	// Construct socket server.
	server := socket.NewServer(da, log, socket.WithShutdownDeadline(2*time.Second))

	// Create a cancellable context to simulate SIGTERM.
	ctx, cancel := context.WithCancel(context.Background())

	// Start server in a goroutine.
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Run(ctx, sockPath)
	}()

	// Wait for the socket to appear.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("socket did not appear within 3s")
		default:
		}
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Send a status request via the socket.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	reqLine := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"status","params":{}}` + "\n")
	if _, err := conn.Write([]byte(reqLine)); err != nil {
		conn.Close()
		t.Fatalf("write: %v", err)
	}

	// Read response.
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	conn.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, buf[:n])
	}
	if resp.Error != nil {
		t.Fatalf("status returned error: %s", *resp.Error)
	}

	// Cancel the context to simulate SIGTERM.
	cancel()

	// Assert server returns within 5s.
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s")
	}

	// Assert socket file is gone (cleaned up by server).
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file still exists after shutdown")
	}

	// Clean up bridge.
	bridgeCancel()
	da.Close()
}
