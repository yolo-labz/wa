package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestAdminReloadAtomicSwap writes a fresh allowlist.toml and asserts
// that calling the admin.reload handler swaps in the new entries.
func TestAdminReloadAtomicSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allowlist.toml")

	initial := `
[[rules]]
jid = "15551234567@s.whatsapp.net"
actions = ["send"]
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	al, err := loadAllowlist(path)
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}
	if al.Size() != 1 {
		t.Fatalf("initial size = %d, want 1", al.Size())
	}

	updated := `
[[rules]]
jid = "15551234567@s.whatsapp.net"
actions = ["send", "read"]

[[rules]]
jid = "15559998888@s.whatsapp.net"
actions = ["send"]
`
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var mu sync.RWMutex
	audit := memory.New(nil)
	handler := handleAdminReload(al, &mu, path, audit, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	raw, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	var res reloadResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Schema != "wa.admin.reload/v1" {
		t.Errorf("schema = %q", res.Schema)
	}
	if res.Entries != 2 {
		t.Errorf("entries = %d, want 2", res.Entries)
	}
	if al.Size() != 2 {
		t.Errorf("in-memory size = %d, want 2", al.Size())
	}
}

// TestAdminReloadParseErrorKeepsPrevious asserts that a malformed TOML
// does not corrupt the in-memory allowlist — reload returns an error
// and the previous state survives.
func TestAdminReloadParseErrorKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allowlist.toml")

	ok := `
[[rules]]
jid = "15551234567@s.whatsapp.net"
actions = ["send"]
`
	if err := os.WriteFile(path, []byte(ok), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	al, err := loadAllowlist(path)
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}

	if err := os.WriteFile(path, []byte("this is not toml = ="), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var mu sync.RWMutex
	audit := memory.New(nil)
	handler := handleAdminReload(al, &mu, path, audit, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if _, err := handler(context.Background(), nil); err == nil {
		t.Fatalf("want error on malformed TOML, got nil")
	}
	if al.Size() != 1 {
		t.Errorf("allowlist corrupted: size = %d, want 1", al.Size())
	}

	// AuditReload wire tag sanity.
	if domain.AuditReload.String() != "reload" {
		t.Errorf("AuditReload.String() = %q, want %q", domain.AuditReload.String(), "reload")
	}
}
