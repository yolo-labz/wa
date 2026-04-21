package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestConfigFeaturesPrintsState exercises `wa config features` (FR-100 /
// FR-114 / FR-122 / T3-01). Asserts the CLI issues `config.features` once
// and surfaces the daemon's resolved flag state under the versioned schema
// envelope.
func TestConfigFeaturesPrintsState(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("config.features", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{
			"embeddings":     false,
			"scheduledSends": true,
			"labels":         false,
		}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"config", "features",
	)

	if !strings.Contains(stdout, `"schema":"wa.config.features/v1"`) {
		t.Fatalf("stdout missing schema envelope:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, `"scheduledSends":true`) {
		t.Fatalf("stdout missing scheduled sends on:\nstdout=%q", stdout)
	}
	if !strings.Contains(stdout, `"embeddings":false`) {
		t.Fatalf("stdout missing embeddings off:\nstdout=%q", stdout)
	}
	if calls := fd.seen(); len(calls) != 1 || calls[0].Method != "config.features" {
		t.Fatalf("expected 1 config.features call, got %+v", calls)
	}
}

func TestConfigFeaturesHumanRender(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("config.features", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{
			"embeddings":     true,
			"scheduledSends": false,
			"labels":         true,
		}, nil
	})
	stdout, _ := runCmd(t,
		"--socket", fd.path(),
		"config", "features",
	)
	for _, want := range []string{"embeddings", "on", "scheduled_sends", "off", "labels"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, stdout)
		}
	}
}
