package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

func newEmbedsDispatcher(t *testing.T, cfg app.DispatcherConfig) *app.Dispatcher {
	t.Helper()
	if cfg.Events == nil {
		cfg.Events = blockingEvents{}
	}
	if cfg.Allowlist == nil {
		cfg.Allowlist = allowAll{}
	}
	if cfg.Audit == nil {
		cfg.Audit = nopAudit{}
	}
	if cfg.Profile == "" {
		cfg.Profile = "default"
	}
	if cfg.SessionCreated.IsZero() {
		cfg.SessionCreated = time.Now().Add(-30 * 24 * time.Hour)
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestEmbeddingsStatusFlagOff — status works with flag off and reports
// enabled=false, size=0.
func TestEmbeddingsStatusFlagOff(t *testing.T) {
	t.Parallel()
	d := newEmbedsDispatcher(t, app.DispatcherConfig{})
	raw, err := d.Handle(context.Background(), "embeddings.status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var r struct {
		Enabled bool `json:"enabled"`
		Size    int  `json:"size"`
	}
	_ = json.Unmarshal(raw, &r)
	if r.Enabled || r.Size != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

// TestEmbeddingsStatusWithIndex — status surfaces model, dim, size.
func TestEmbeddingsStatusWithIndex(t *testing.T) {
	t.Parallel()
	idx := newStubIndex()
	d := newEmbedsDispatcher(t, app.DispatcherConfig{
		Features:    app.FeatureFlags{Embeddings: true},
		VectorIndex: idx,
		Embedder:    &stubEmbedder{},
	})
	raw, err := d.Handle(context.Background(), "embeddings.status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var r struct {
		Enabled bool   `json:"enabled"`
		Model   string `json:"model"`
		Dim     int    `json:"dim"`
		Size    int    `json:"size"`
	}
	_ = json.Unmarshal(raw, &r)
	if !r.Enabled || r.Model != "stub-4" || r.Dim != 4 {
		t.Fatalf("unexpected: %+v", r)
	}
}

// TestEmbeddingsPurgeFlagOff returns -32113.
func TestEmbeddingsPurgeFlagOff(t *testing.T) {
	t.Parallel()
	d := newEmbedsDispatcher(t, app.DispatcherConfig{})
	_, err := d.Handle(context.Background(), "embeddings.purge", json.RawMessage(`{}`))
	if !errors.Is(err, app.ErrEmbeddingsDisabled) {
		t.Fatalf("flag-off: got %v want ErrEmbeddingsDisabled", err)
	}
}

// TestEmbeddingsPurgeClearsIndex — with flag on + index wired, purge
// drops every vector.
func TestEmbeddingsPurgeClearsIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	idx := newStubIndex()
	_ = idx.Upsert(ctx, stubEmbeddingFor("m1"))
	_ = idx.Upsert(ctx, stubEmbeddingFor("m2"))
	if n := idx.size(); n != 2 {
		t.Fatalf("pre-purge size: %d", n)
	}

	d := newEmbedsDispatcher(t, app.DispatcherConfig{
		Features:    app.FeatureFlags{Embeddings: true},
		VectorIndex: idx,
	})
	raw, err := d.Handle(ctx, "embeddings.purge", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !bytesContains(raw, `"purged":true`) {
		t.Fatalf("result missing purged=true: %s", raw)
	}
	if n := idx.size(); n != 0 {
		t.Fatalf("post-purge size: %d", n)
	}
}

func bytesContains(b []byte, sub string) bool {
	return len(b) > 0 && (func() bool {
		for i := 0; i+len(sub) <= len(b); i++ {
			if string(b[i:i+len(sub)]) == sub {
				return true
			}
		}
		return false
	}())
}
