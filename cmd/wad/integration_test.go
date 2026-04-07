//go:build integration

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

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// TestIntegrationFullWiring constructs wad in-process with the memory
// adapter (not whatsmeow), starts the socket server, exercises the
// pair -> allow add -> send cycle via a raw socket client, verifies
// responses, then cancels the context and asserts clean shutdown.
//
// Gated behind //go:build integration AND WA_INTEGRATION=1.
func TestIntegrationFullWiring(t *testing.T) {
	if os.Getenv("WA_INTEGRATION") != "1" {
		t.Skip("requires WA_INTEGRATION=1")
	}

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "wa.sock")

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Build full wiring with memory adapter.
	mem := memory.New(nil)

	// Pre-seed: grant an allowlisted JID for the send test.
	testJID, err := domain.ParseJID("5511999990000@s.whatsapp.net")
	if err != nil {
		t.Fatalf("parse JID: %v", err)
	}
	mem.Grant(testJID, domain.ActionSend, domain.ActionRead)

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

	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	da := newDispatcherAdapter(bridgeCtx, dispatcher, nil)

	server := socket.NewServer(da, log, socket.WithShutdownDeadline(2*time.Second))

	ctx, cancel := context.WithCancel(context.Background())

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Run(ctx, sockPath)
	}()

	// Wait for socket.
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

	// Helper: send a JSON-RPC request and return the response.
	rpcCall := func(t *testing.T, method string, params any) json.RawMessage {
		t.Helper()
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		p, _ := json.Marshal(params)
		line := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`+"\n", method, p)
		if _, err := conn.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}

		buf := make([]byte, 8192)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}

		var resp struct {
			Result json.RawMessage  `json:"result"`
			Error  *json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			t.Fatalf("unmarshal: %v (raw: %s)", err, buf[:n])
		}
		if resp.Error != nil {
			t.Fatalf("%s returned error: %s", method, *resp.Error)
		}
		return resp.Result
	}

	// Step 1: pair (memory adapter returns success).
	t.Run("pair", func(t *testing.T) {
		result := rpcCall(t, "pair", map[string]any{})
		var obj map[string]any
		if err := json.Unmarshal(result, &obj); err != nil {
			t.Fatalf("unmarshal pair result: %v", err)
		}
		if paired, _ := obj["paired"].(bool); !paired {
			t.Errorf("pair: expected paired=true, got %v", obj)
		}
	})

	// Step 2: status.
	t.Run("status", func(t *testing.T) {
		result := rpcCall(t, "status", map[string]any{})
		if len(result) == 0 {
			t.Fatal("status returned empty result")
		}
	})

	// Step 3: send to the pre-allowed JID.
	t.Run("send", func(t *testing.T) {
		result := rpcCall(t, "send", map[string]any{
			"to":   "5511999990000@s.whatsapp.net",
			"body": "integration test message",
		})
		var obj map[string]any
		if err := json.Unmarshal(result, &obj); err != nil {
			t.Fatalf("unmarshal send result: %v", err)
		}
		if _, ok := obj["messageId"]; !ok {
			t.Errorf("send: expected messageId in result, got %v", obj)
		}
	})

	// Step 4: groups (should return empty list from memory adapter).
	t.Run("groups", func(t *testing.T) {
		result := rpcCall(t, "groups", map[string]any{})
		var obj map[string]any
		if err := json.Unmarshal(result, &obj); err != nil {
			t.Fatalf("unmarshal groups result: %v", err)
		}
	})

	// Clean shutdown.
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s")
	}

	bridgeCancel()
	da.Close()

	// Verify socket cleaned up.
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("socket file still exists after shutdown")
	}
}
