package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// privacySetParams is the JSON-RPC params for "privacy.set" (FR-029). Keys
// and values use their canonical wire tokens (groups|readReceipts|lastSeen|
// profile|about) x (everyone|contacts|nobody).
type privacySetParams struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// privacyTupleDTO is one (key, value) pair in a privacy.get result.
type privacyTupleDTO struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// privacyGetResult is the JSON-RPC result for "privacy.get" (FR-030). The
// list is the live server-side set; the outer object allows future fields
// (e.g. a revision counter) without breaking clients.
type privacyGetResult struct {
	Settings []privacyTupleDTO `json:"settings"`
}

// handlePrivacySet implements "privacy.set" (FR-029). Idempotency-wrapped
// because a replay of the same (key, value) tuple must not double-audit.
func (d *Dispatcher) handlePrivacySet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "privacy.set", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doPrivacySet(ctx, raw)
	})
}

func (d *Dispatcher) doPrivacySet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.privacy == nil {
		return nil, ErrMethodNotFound
	}
	var p privacySetParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Key == "" || p.Value == "" {
		return nil, ErrInvalidParams
	}
	key, err := domain.ParsePrivacyKey(p.Key)
	if err != nil {
		return nil, ErrInvalidParams
	}
	value, err := domain.ParsePrivacyValue(p.Value)
	if err != nil {
		return nil, ErrInvalidParams
	}
	tup := domain.PrivacyTuple{Key: key, Value: value}
	if err := d.privacy.Set(ctx, tup); err != nil {
		return nil, fmt.Errorf("privacy.set: %w", err)
	}
	return json.Marshal(struct{}{})
}

// handlePrivacyGet implements "privacy.get" (FR-030). Read-only — no
// idempotency sidecar. The port guarantees a live server read (no local
// cache) so out-of-band changes from the phone UI surface immediately.
func (d *Dispatcher) handlePrivacyGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.privacy == nil {
		return nil, ErrMethodNotFound
	}
	tuples, err := d.privacy.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("privacy.get: %w", err)
	}
	out := make([]privacyTupleDTO, 0, len(tuples))
	for _, t := range tuples {
		out = append(out, privacyTupleDTO{Key: t.Key.String(), Value: t.Value.String()})
	}
	return json.Marshal(privacyGetResult{Settings: out})
}
