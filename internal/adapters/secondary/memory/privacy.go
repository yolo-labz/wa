package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/yolo-labz/wa/internal/domain"
)

// PrivacySettings is the in-memory implementation of
// app.PrivacySettings (feature 018 FR-029/FR-030).
type PrivacySettings struct {
	mu       sync.Mutex
	settings map[domain.PrivacyKey]domain.PrivacyValue
}

// NewPrivacySettings returns an empty fake (no defaults; Get returns an
// empty slice until Set is called).
func NewPrivacySettings() *PrivacySettings {
	return &PrivacySettings{settings: make(map[domain.PrivacyKey]domain.PrivacyValue)}
}

// Set implements app.PrivacySettings.
func (p *PrivacySettings) Set(ctx context.Context, tuple domain.PrivacyTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tuple.Validate(); err != nil {
		return fmt.Errorf("PrivacySettings.Set: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.settings[tuple.Key] = tuple.Value
	return nil
}

// Get implements app.PrivacySettings. Returns tuples sorted by Key.String()
// for deterministic output.
func (p *PrivacySettings) Get(ctx context.Context) ([]domain.PrivacyTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]domain.PrivacyTuple, 0, len(p.settings))
	for k, v := range p.settings {
		out = append(out, domain.PrivacyTuple{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out, nil
}
