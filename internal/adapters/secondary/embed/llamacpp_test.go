package embed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLlamaCppDetect verifies the exec.LookPath branch surfaces
// ErrBinaryMissing when llama-server is absent. We cannot reliably
// assume the binary is present on any given CI worker, so we assert
// the error shape, not the success path.
func TestLlamaCppDetect(t *testing.T) {
	t.Parallel()
	l := NewLlamaCpp()
	err := l.Detect()
	if err != nil && !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	// Either llama-server exists (err nil) or it doesn't (ErrBinaryMissing).
	// Both are valid.
}

// TestEmbedDims384 — legacy single-doc {"embedding":[...]} shape decodes
// into a 384-dim unit vector. Matches tasks-tier3.md T3-03 test name.
func TestEmbedDims384(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embedding" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// 384 evenly distributed values — post-normalisation should sum
		// to ≈ 1 in L2.
		var sb strings.Builder
		sb.WriteString(`{"embedding":[`)
		for i := range 384 {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("0.05")
		}
		sb.WriteString(`]}`)
		_, _ = w.Write([]byte(sb.String()))
	}))
	t.Cleanup(srv.Close)

	l := &LlamaCpp{
		Endpoint: srv.URL + "/embedding",
		Model:    "bge-small-384",
		Dim:      384,
	}
	emb, err := l.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(emb.Vec) != 384 {
		t.Fatalf("dim: got %d want 384", len(emb.Vec))
	}
	// MessageID intentionally unset at the adapter layer; Validate() will
	// reject this — we only exercise the dim/norm checks here.
	_ = emb.Validate()
	// L2 norm should be ≈ 1.
	var sq float32
	for _, x := range emb.Vec {
		sq += x * x
	}
	if sq < 0.99 || sq > 1.01 {
		t.Fatalf("normalisation: L2² = %v (want ~1)", sq)
	}
}

// TestLlamaCppBatchWrappedShape — newer llama-server versions return
// an array of objects with matrix embeddings. Adapter must decode both.
func TestLlamaCppBatchWrappedShape(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		sb.WriteString(`[{"embedding":[[`)
		for i := range 4 {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("0.5")
		}
		sb.WriteString(`]]}]`)
		_, _ = w.Write([]byte(sb.String()))
	}))
	t.Cleanup(srv.Close)

	l := &LlamaCpp{Endpoint: srv.URL + "/embedding", Model: "stub-4", Dim: 4}
	emb, err := l.Embed(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(emb.Vec) != 4 {
		t.Fatalf("dim: got %d want 4", len(emb.Vec))
	}
}
