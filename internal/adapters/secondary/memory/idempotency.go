package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// IdempotencyStore is the in-memory implementation of app.IdempotencyStore
// per FR-030..FR-033. Keys are scoped by (profile, key.Value); fingerprint
// mismatches surface as domain.ErrIdempotencyConflict.
type IdempotencyStore struct {
	mu   sync.Mutex
	rows map[idempotencyRowKey]idempotencyRow
}

type idempotencyRowKey struct {
	profile string
	value   string
}

type idempotencyRow struct {
	fingerprint [32]byte
	result      []byte
	expiresAt   time.Time
}

// NewIdempotencyStore returns an empty in-memory IdempotencyStore.
func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{rows: make(map[idempotencyRowKey]idempotencyRow)}
}

// Check implements app.IdempotencyStore.
func (s *IdempotencyStore) Check(ctx context.Context, profile string, key domain.IdempotencyKey) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := key.Validate(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[idempotencyRowKey{profile: profile, value: key.Value}]
	if !ok {
		return nil, false, nil
	}
	if row.fingerprint != key.Fingerprint {
		return nil, false, fmt.Errorf("%w: key=%s", domain.ErrIdempotencyConflict, key.Value)
	}
	out := make([]byte, len(row.result))
	copy(out, row.result)
	return out, true, nil
}

// Record implements app.IdempotencyStore.
func (s *IdempotencyStore) Record(ctx context.Context, profile string, key domain.IdempotencyKey, result []byte, expiresAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	buf := make([]byte, len(result))
	copy(buf, result)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[idempotencyRowKey{profile: profile, value: key.Value}] = idempotencyRow{
		fingerprint: key.Fingerprint,
		result:      buf,
		expiresAt:   expiresAt,
	}
	return nil
}

// Sweep implements app.IdempotencyStore.
func (s *IdempotencyStore) Sweep(ctx context.Context, before time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, row := range s.rows {
		if !row.expiresAt.After(before) {
			delete(s.rows, k)
			n++
		}
	}
	return n, nil
}
