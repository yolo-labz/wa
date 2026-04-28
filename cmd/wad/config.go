package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// configFile is the on-disk TOML schema for per-profile config.toml.
// Only the [features] block is defined in v0.5 (feature 017 / T3-01).
type configFile struct {
	Features featuresBlock `toml:"features"`
}

// featuresBlock uses *bool so a missing key falls back to the default.
// TOML unmarshalling zero-values booleans silently, which would make
// "scheduled_sends = false" indistinguishable from "key omitted".
type featuresBlock struct {
	Embeddings     *bool `toml:"embeddings"`
	ScheduledSends *bool `toml:"scheduled_sends"`
	Labels         *bool `toml:"labels"`
}

// Config is the resolved daemon configuration.
type Config struct {
	Features app.FeatureFlags
}

// loadConfig reads config.toml and merges overrides onto the defaults.
// If the file does not exist, DefaultFeatureFlags() is returned unchanged.
func loadConfig(path string) (*Config, error) {
	flags := app.DefaultFeatureFlags()

	data, err := os.ReadFile(path) //nolint:gosec // path is per-profile XDG config
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Features: flags}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loadConfig: read %s: %w", path, err)
	}

	var cf configFile
	if err := toml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("loadConfig: parse %s: %w", path, err)
	}

	if cf.Features.Embeddings != nil {
		flags.Embeddings = *cf.Features.Embeddings
	}
	if cf.Features.ScheduledSends != nil {
		flags.ScheduledSends = *cf.Features.ScheduledSends
	}
	if cf.Features.Labels != nil {
		flags.Labels = *cf.Features.Labels
	}

	return &Config{Features: flags}, nil
}
