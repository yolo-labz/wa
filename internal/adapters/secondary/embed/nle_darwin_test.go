//go:build darwin

package embed

import (
	"bufio"
	"strings"
	"testing"
)

// TestNleHelperRoundTrip — decode a canonical helper response without
// spawning the real Swift binary. Matches tasks-tier3.md T3-05 test name.
func TestNleHelperRoundTrip(t *testing.T) {
	t.Parallel()
	line := `{"embedding":[0.1,0.2,0.3,0.4],"model":"darwin-nle-sentence-512","dim":4}` + "\n"
	r := bufio.NewReader(strings.NewReader(line))
	emb, err := decodeNLEResponse(r, "darwin-nle-sentence-512", 4)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if emb.Model != "darwin-nle-sentence-512" {
		t.Fatalf("model: got %q", emb.Model)
	}
	if emb.Dim != 4 {
		t.Fatalf("dim: got %d", emb.Dim)
	}
	if len(emb.Vec) != 4 {
		t.Fatalf("vec len: got %d", len(emb.Vec))
	}
	// L2 norm ~1.
	var sq float32
	for _, x := range emb.Vec {
		sq += x * x
	}
	if sq < 0.99 || sq > 1.01 {
		t.Fatalf("normalisation: L2² = %v (want ~1)", sq)
	}
}

// TestNleHelperDimMismatch — wrong-dim response is rejected.
func TestNleHelperDimMismatch(t *testing.T) {
	t.Parallel()
	line := `{"embedding":[0.1,0.2],"model":"x","dim":2}` + "\n"
	r := bufio.NewReader(strings.NewReader(line))
	_, err := decodeNLEResponse(r, "x", 4)
	if err == nil {
		t.Fatalf("expected dim-mismatch error, got nil")
	}
}
