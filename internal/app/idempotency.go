package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// IdempotencyExec wraps a side-effecting callback in the FR-030..FR-033
// replay protocol:
//
//  1. Compute the canonical fingerprint of params (reserved fields
//     stripped).
//  2. Check the store; on match return the cached result with
//     replayed=true.
//  3. On fingerprint conflict return domain.ErrIdempotencyConflict.
//  4. On miss invoke fn and persist its result with now+24h TTL.
//
// Callers supply params as a marshallable Go value; the helper does the
// canonicalisation (sorted keys, numerics preserved) so that callers
// cannot drift the hash by accident.
func IdempotencyExec[T any](
	ctx context.Context,
	store IdempotencyStore,
	profile string,
	keyValue string,
	params any,
	now time.Time,
	fn func(ctx context.Context) (T, error),
) (result T, replayed bool, err error) {
	var zero T
	canonical, err := canonicalJSON(params)
	if err != nil {
		return zero, false, fmt.Errorf("idempotency: canonicalise params: %w", err)
	}
	fp := domain.ComputeFingerprint(canonical)
	key, err := domain.NewIdempotencyKey(keyValue, fp)
	if err != nil {
		return zero, false, err
	}
	cached, hit, err := store.Check(ctx, profile, key)
	if err != nil {
		return zero, false, err
	}
	if hit {
		if err := json.Unmarshal(cached, &result); err != nil {
			return zero, false, fmt.Errorf("idempotency: decode cached result: %w", err)
		}
		return result, true, nil
	}
	result, err = fn(ctx)
	if err != nil {
		return zero, false, err
	}
	buf, err := json.Marshal(result)
	if err != nil {
		return zero, false, fmt.Errorf("idempotency: encode result: %w", err)
	}
	expires := now.Add(time.Duration(domain.IdempotencyKeyTTL) * time.Second)
	if err := store.Record(ctx, profile, key, buf, expires); err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) {
			return zero, false, err
		}
		return zero, false, fmt.Errorf("idempotency: record result: %w", err)
	}
	return result, false, nil
}

// canonicalJSON marshals v into a deterministic, sort-keyed byte slice
// suitable for fingerprinting. Reserved request fields (idempotencyKey,
// requestId, timestamps starting with `ts`) are stripped at the top level
// per FR-033.
//
// Fast path: when v is already a JSON-shaped value (map[string]any,
// []any, scalar) the function skips the redundant Marshal/Unmarshal
// round-trip and operates directly on the input. The top-level
// map[string]any case shallow-copies so stripReserved does not mutate
// the caller's map; nested values are NOT copied because stripReserved
// only deletes top-level keys (verified 2026-04-26).
//
// Slow path: arbitrary input types still round-trip via JSON to
// preserve the prior contract for typed structs / json.Marshaler.
func canonicalJSON(v any) ([]byte, error) {
	decoded, err := normalizeForCanonical(v)
	if err != nil {
		return nil, err
	}
	stripped := stripReserved(decoded)
	return marshalCanonical(stripped)
}

// normalizeForCanonical returns a JSON-shape value for v. Inputs that
// are already JSON-shaped (per the type switch) pass through with a
// top-level shallow copy when applicable; arbitrary types fall back to
// the JSON round-trip path.
func normalizeForCanonical(v any) (any, error) {
	switch t := v.(type) {
	case nil, bool, string, float64, int, int64, json.Number, []byte:
		return t, nil
	case map[string]any:
		// Shallow copy so stripReserved cannot mutate the caller.
		// Nested values stay aliased — stripReserved only touches
		// top-level keys.
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = val
		}
		return m, nil
	case []any:
		return t, nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var d any
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		return d, nil
	}
}

var reservedFields = map[string]struct{}{
	"idempotencyKey": {},
	"requestId":      {},
	"ts":             {},
	"timestamp":      {},
}

func stripReserved(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k := range reservedFields {
		delete(m, k)
	}
	return m
}

func marshalCanonical(v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf = append(buf, kb...)
			buf = append(buf, ':')
			vb, err := marshalCanonical(x[k])
			if err != nil {
				return nil, err
			}
			buf = append(buf, vb...)
		}
		buf = append(buf, '}')
		return buf, nil
	case []any:
		buf := []byte{'['}
		for i, item := range x {
			if i > 0 {
				buf = append(buf, ',')
			}
			ib, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			buf = append(buf, ib...)
		}
		buf = append(buf, ']')
		return buf, nil
	default:
		return json.Marshal(x)
	}
}
