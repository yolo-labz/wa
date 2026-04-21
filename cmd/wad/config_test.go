package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
)

func TestLoadConfigMissingReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Features != app.DefaultFeatureFlags() {
		t.Fatalf("want defaults, got %+v", cfg.Features)
	}
}

func TestLoadConfigParsesOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := []byte(`[features]
embeddings = true
scheduled_sends = false
labels = true
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	want := app.FeatureFlags{Embeddings: true, ScheduledSends: false, Labels: true}
	if cfg.Features != want {
		t.Fatalf("features = %+v, want %+v", cfg.Features, want)
	}
}

func TestLoadConfigPartialOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := []byte(`[features]
embeddings = true
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	want := app.FeatureFlags{Embeddings: true, ScheduledSends: true, Labels: false}
	if cfg.Features != want {
		t.Fatalf("features = %+v, want %+v", cfg.Features, want)
	}
}
