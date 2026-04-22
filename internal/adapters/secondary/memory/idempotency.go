package memory

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// IdempotencyStore is the in-memory implementation of app.IdempotencyStore
// per FR-030..FR-033 (017) and FR-034a (018). Feature-017 rows live in
// `rows` keyed by (profile, key.Value); FR-034a rows live in `v4Rows`
// keyed by (method, profile, key). They share a single Sweep implementation
// so tests can drive either surface.
type IdempotencyStore struct {
	mu     sync.Mutex
	rows   map[idempotencyRowKey]idempotencyRow
	v4Rows map[v4Key]v4Row
}

type v4Key struct {
	method, profile, key string
}

type v4Row struct {
	paramsHash [32]byte
	result     []byte
	expiresAt  time.Time
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
	return &IdempotencyStore{
		rows:   make(map[idempotencyRowKey]idempotencyRow),
		v4Rows: make(map[v4Key]v4Row),
	}
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
	for k, row := range s.v4Rows {
		if !row.expiresAt.After(before) {
			delete(s.v4Rows, k)
			n++
		}
	}
	return n, nil
}

// LoadOrStore implements the FR-034a entry point in the memory fake.
// Same contract as the sqlite sidecar: empty key disables caching,
// mismatched paramsHash yields domain.ErrIdempotencyCollision, match
// replays cached bytes, miss executes + caches with 24h TTL.
func (s *IdempotencyStore) LoadOrStore(
	ctx context.Context,
	method string,
	profile string,
	key string,
	paramsHash [32]byte,
	execute func() ([]byte, error),
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return execute()
	}
	s.mu.Lock()
	if s.v4Rows == nil {
		s.v4Rows = make(map[v4Key]v4Row)
	}
	rowKey := v4Key{method: method, profile: profile, key: key}
	now := time.Now().UTC()
	if row, ok := s.v4Rows[rowKey]; ok && row.expiresAt.After(now) {
		s.mu.Unlock()
		if !bytes.Equal(row.paramsHash[:], paramsHash[:]) {
			return nil, fmt.Errorf("%w: method=%s key=%s", domain.ErrIdempotencyCollision, method, key)
		}
		out := make([]byte, len(row.result))
		copy(out, row.result)
		return out, nil
	}
	s.mu.Unlock()
	// Execute without lock so the callback can't deadlock on LoadOrStore.
	bytesOut, execErr := execute()
	if execErr != nil {
		return nil, execErr
	}
	buf := make([]byte, len(bytesOut))
	copy(buf, bytesOut)
	s.mu.Lock()
	s.v4Rows[rowKey] = v4Row{
		paramsHash: paramsHash,
		result:     buf,
		expiresAt:  now.Add(24 * time.Hour),
	}
	s.mu.Unlock()
	return bytesOut, nil
}
