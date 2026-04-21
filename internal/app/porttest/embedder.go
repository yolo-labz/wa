package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
)

// RunEmbedderContract exercises the FR-102 Embedder contract (EB1..EB4).
// Adapters satisfy the suite by passing a factory that returns a fresh
// instance per sub-test.
func RunEmbedderContract(t *testing.T, factory func(t *testing.T) app.Embedder) {
	t.Helper()

	t.Run("EB1_info_stable", func(t *testing.T) {
		e := factory(t)
		a := e.Info()
		b := e.Info()
		if a != b {
			reportf(t, "Embedder", "Info", "EB1", "stable info", "values differed across calls")
		}
		if a.Model == "" {
			reportf(t, "Embedder", "Info", "EB1", "non-empty model", "empty")
		}
		if a.Dim <= 0 {
			reportf(t, "Embedder", "Info", "EB1", "positive dim", "non-positive")
		}
	})

	t.Run("EB2_embed_dim_matches_info", func(t *testing.T) {
		e := factory(t)
		info := e.Info()
		emb, err := e.Embed(context.Background(), "ping")
		if err != nil {
			reportf(t, "Embedder", "Embed", "EB2", "nil err", err.Error())
			return
		}
		if emb.Dim != info.Dim || len(emb.Vec) != info.Dim {
			reportf(t, "Embedder", "Embed", "EB2", "dim == info.Dim", "mismatch")
		}
		if emb.Model != info.Model {
			reportf(t, "Embedder", "Embed", "EB2", "model == info.Model", "mismatch")
		}
	})

	t.Run("EB3_cancelled_ctx", func(t *testing.T) {
		e := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.Embed(ctx, "ping"); err == nil {
			reportf(t, "Embedder", "Embed", "EB3", "non-nil err after cancel", "nil")
		}
	})

	t.Run("EB4_unit_norm_ish", func(t *testing.T) {
		e := factory(t)
		emb, err := e.Embed(context.Background(), "hello world")
		if err != nil {
			reportf(t, "Embedder", "Embed", "EB4", "nil err", err.Error())
			return
		}
		var sq float32
		for _, v := range emb.Vec {
			sq += v * v
		}
		// Unit-normalised vectors have ||v||² == 1 (± float32 drift).
		if sq < 0.95 || sq > 1.05 {
			reportf(t, "Embedder", "Embed", "EB4", "unit-normalised vector", "||v||² outside [0.95,1.05]")
		}
	})
}
