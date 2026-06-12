package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
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

// TestHealthDegradedComponents asserts SF-03: best-effort subsystems the
// composition root lost at startup surface through the health result, and
// the field is omitted entirely when fully provisioned.
func TestHealthDegradedComponents(t *testing.T) {
	adapter := memory.New(nil)
	base := app.DispatcherConfig{
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

	degradedCfg := base
	degradedCfg.DegradedComponents = []string{"contacts", "events"}
	d := app.NewDispatcher(degradedCfg)
	t.Cleanup(func() { _ = d.Close() })

	raw, err := d.Handle(context.Background(), "health", nil)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	var h struct {
		Degraded []string `json:"degraded"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(h.Degraded) != 2 || h.Degraded[0] != "contacts" || h.Degraded[1] != "events" {
		t.Errorf("degraded = %v, want [contacts events]", h.Degraded)
	}

	healthy := app.NewDispatcher(base)
	t.Cleanup(func() { _ = healthy.Close() })
	raw2, err := healthy.Handle(context.Background(), "health", nil)
	if err != nil {
		t.Fatalf("health (healthy): %v", err)
	}
	if bytes.Contains(raw2, []byte(`"degraded"`)) {
		t.Errorf("degraded key present on fully-provisioned daemon: %s", raw2)
	}
}

// TestHealthStreamErrorFieldsOmittedWhenClean — SF-08: the additive
// streamErrors/lastStreamErrorTs fields stay omitted while the bridge
// has observed no stream failures (omitempty preserves wa.health/v1).
// The populated path is pinned at the bridge layer in
// TestEventBridge_StreamErrorSignalAndRecovery.
func TestHealthStreamErrorFieldsOmittedWhenClean(t *testing.T) {
	adapter := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{
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
	})
	t.Cleanup(func() { _ = d.Close() })

	raw, err := d.Handle(context.Background(), "health", nil)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	for _, key := range []string{`"streamErrors"`, `"lastStreamErrorTs"`} {
		if bytes.Contains(raw, []byte(key)) {
			t.Errorf("%s present on clean daemon: %s", key, raw)
		}
	}
}
