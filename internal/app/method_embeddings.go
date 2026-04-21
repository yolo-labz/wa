package app

import (
	"context"
	"encoding/json"
	"fmt"
)

// embeddingsStatusView is the wire shape for embeddings.status.
type embeddingsStatusView struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
	Dim     int    `json:"dim,omitempty"`
	Size    int    `json:"size"`
}

// handleEmbeddingsStatus reports the current vector index state. Safe
// to call with the flag off (returns enabled=false, size=0) so admin
// tooling can probe without tripping -32113. T3-13.
func (d *Dispatcher) handleEmbeddingsStatus(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	v := embeddingsStatusView{Enabled: d.features.Embeddings}
	if d.embedder != nil {
		info := d.embedder.Info()
		v.Model = info.Model
		v.Dim = info.Dim
	}
	if d.vectorIndex != nil {
		n, err := d.vectorIndex.Size(ctx)
		if err != nil {
			return nil, fmt.Errorf("embeddings.status: size: %w", err)
		}
		v.Size = n
	}
	return marshalResult(v)
}

// handleEmbeddingsPurge drops every vector. Rejects when embeddings
// flag is off or when no index is wired. T3-13.
func (d *Dispatcher) handleEmbeddingsPurge(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if !d.features.Embeddings {
		return nil, ErrEmbeddingsDisabled
	}
	if d.vectorIndex == nil {
		return nil, ErrEmbeddingsDisabled
	}
	if err := d.vectorIndex.Purge(ctx); err != nil {
		return nil, fmt.Errorf("embeddings.purge: %w", err)
	}
	return marshalResult(struct {
		Purged bool `json:"purged"`
	}{Purged: true})
}
