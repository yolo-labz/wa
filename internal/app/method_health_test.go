package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
)

// TestHealthReturnsSchema asserts the health handler returns a
// well-formed result under 1 second with the documented schema string.
func TestHealthReturnsSchema(t *testing.T) {
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		Profile:        "unit-test-profile",
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })

	t0 := time.Now()
	raw, err := d.Handle(context.Background(), "health", nil)
	elapsed := time.Since(t0)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("health took %s (>500ms) — SC-008 liveness probe", elapsed)
	}

	var h struct {
		Schema  string `json:"schema"`
		Profile string `json:"profile"`
		Paired  bool   `json:"paired"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if h.Schema != "wa.health/v1" {
		t.Errorf("schema = %q, want %q", h.Schema, "wa.health/v1")
	}
	if h.Profile != "unit-test-profile" {
		t.Errorf("profile = %q, want %q", h.Profile, "unit-test-profile")
	}
}
