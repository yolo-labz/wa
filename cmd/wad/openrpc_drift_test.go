package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/agentdocs"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestOpenRPCMethodsExist pins the /openrpc.json catalog against the
// live dispatcher surface (feature 111 / roadmap 1.2): every method we
// document MUST exist, so the published contract can never describe a
// method the daemon would answer with -32601. (Coverage is one-way by
// design — the daemon has flag-gated/admin methods the public catalog
// intentionally omits.)
func TestOpenRPCMethodsExist(t *testing.T) {
	t.Parallel()

	var doc struct {
		Methods []struct {
			Name string `json:"name"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(agentdocs.OpenRPCJSON, &doc); err != nil {
		t.Fatalf("openrpc.json invalid: %v", err)
	}
	if len(doc.Methods) < 25 {
		t.Fatalf("openrpc.json lists %d methods — catalog suspiciously small", len(doc.Methods))
	}

	mem := memory.New(nil)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: mem, Events: mem, Contacts: mem, Groups: mem, Session: mem,
		Allowlist: mem, Audit: mem, History: mem, Pairer: mem,
		SessionCreated: time.Now(), Logger: log,
	})
	t.Cleanup(func() { _ = d.Close() })
	// Adapter-registered methods (mirrors history_methods.go wiring).
	for _, m := range []string{
		"history", "messages", "search", "purge", "export",
		"chat.list", "messages.list", "media.list", "sync.force", "sync.status",
	} {
		d.RegisterMethod(m, nil)
	}
	live := d.Methods()
	// Socket-layer methods handled before dispatch.
	live = append(live, "system.hello")

	for _, m := range doc.Methods {
		if !slices.Contains(live, m.Name) {
			t.Errorf("openrpc.json documents %q but the dispatcher does not register it", m.Name)
		}
	}
}
