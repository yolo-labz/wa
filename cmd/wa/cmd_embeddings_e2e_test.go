package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmbeddingsPurge — e2e smoke over the fakeDaemon harness for
// `wa embeddings status` + `wa embeddings purge`. Matches tasks-tier3.md
// T3-13 test name.
func TestEmbeddingsPurge(t *testing.T) {
	fd := newFakeDaemon(t)

	fd.on("embeddings.status", func(params json.RawMessage) (any, *rpcError) {
		return map[string]any{
			"enabled": true,
			"model":   "bge-small-384",
			"dim":     384,
			"size":    42,
		}, nil
	})
	fd.on("embeddings.purge", func(params json.RawMessage) (any, *rpcError) {
		return map[string]any{"purged": true}, nil
	})

	// --- status (human output) ----------------------------------------
	stdout, _ := runCmd(t, "--socket", fd.path(), "embeddings", "status")
	if !strings.Contains(stdout, "bge-small-384") || !strings.Contains(stdout, "42") {
		t.Fatalf("status stdout: %q", stdout)
	}

	// --- purge without --yes fails client-side ------------------------
	embeddingsYes = false
	_, stderr := runCmd(t, "--socket", fd.path(), "embeddings", "purge")
	if !strings.Contains(stderr, "--yes is required") {
		t.Fatalf("expected --yes gate, stderr=%q", stderr)
	}

	// --- purge --yes round-trips to the daemon ------------------------
	embeddingsYes = false // cobra flag resets between runs
	stdout, _ = runCmd(t, "--socket", fd.path(), "--json", "embeddings", "purge", "--yes")
	if !strings.Contains(stdout, `"purged":true`) {
		t.Fatalf("purge stdout: %q", stdout)
	}

	// --- method sequence ----------------------------------------------
	calls := fd.seen()
	wantSeq := []string{"embeddings.status", "embeddings.purge"}
	if len(calls) != len(wantSeq) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(wantSeq), calls)
	}
	for i, w := range wantSeq {
		if calls[i].Method != w {
			t.Fatalf("call %d: got %q want %q", i, calls[i].Method, w)
		}
	}
}

// TestEmbeddingsDisabledBubbles — the -32113 error from the daemon
// surfaces as a non-empty stderr.
func TestEmbeddingsDisabledBubbles(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("embeddings.purge", func(params json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32113, Message: "embeddings disabled"}
	})
	embeddingsYes = true
	_, stderr := runCmd(t, "--socket", fd.path(), "embeddings", "purge", "--yes")
	if !strings.Contains(stderr, "embeddings disabled") {
		t.Fatalf("expected embeddings_disabled surface, stderr=%q", stderr)
	}
}
