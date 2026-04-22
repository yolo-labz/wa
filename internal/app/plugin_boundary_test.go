package app

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// TestPluginBoundaryRefusesChannelSourced pins the FR-051 refusal list
// against testdata/plugin_boundary/refuse_channel_sourced.json — the
// authoritative contract the wa-assistant MCP shim consumes. The fixture
// and the in-code pluginBoundaryRefused map must stay lock-step; a drift
// between them is how plugin PRs regress the boundary silently.
func TestPluginBoundaryRefusesChannelSourced(t *testing.T) {
	data, err := os.ReadFile("testdata/plugin_boundary/refuse_channel_sourced.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Schema      string            `json:"schema"`
		Requirement string            `json:"requirement"`
		Refused     []string          `json:"refused"`
		Rationale   map[string]string `json:"rationale"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if fixture.Schema != "wa.plugin-boundary/v1" {
		t.Errorf("fixture schema = %q, want wa.plugin-boundary/v1", fixture.Schema)
	}
	if fixture.Requirement != "FR-051" {
		t.Errorf("fixture requirement = %q, want FR-051", fixture.Requirement)
	}

	got := make([]string, 0, len(pluginBoundaryRefused))
	for m := range pluginBoundaryRefused {
		got = append(got, m)
	}
	sort.Strings(got)
	want := append([]string(nil), fixture.Refused...)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("refused set size mismatch: code has %d, fixture has %d\ncode=%v\nfixture=%v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("refused[%d]: code=%q fixture=%q — fixture and map drifted", i, got[i], want[i])
		}
	}

	// Rationale presence: every refused method must have an English
	// justification so plugin PR reviewers can evaluate the threat model
	// without spelunking the spec.
	for _, m := range fixture.Refused {
		if fixture.Rationale[m] == "" {
			t.Errorf("fixture rationale missing for %q", m)
		}
		if !IsPluginBoundaryRefused(m) {
			t.Errorf("IsPluginBoundaryRefused(%q) = false, want true", m)
		}
	}
}
