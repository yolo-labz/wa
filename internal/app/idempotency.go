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
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	stripped := stripReserved(decoded)
	return marshalCanonical(stripped)
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
