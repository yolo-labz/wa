package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeIdempotencyStore is an in-package minimal IdempotencyStore so these
// tests stay in `package app` without importing the memory adapter (which
// imports app, creating a cycle).
type fakeIdempotencyStore struct {
	mu   sync.Mutex
	rows map[string]fakeRow
}

type fakeRow struct {
	key     domain.IdempotencyKey
	result  []byte
	expires time.Time
}

func newFakeIdempotencyStore() *fakeIdempotencyStore {
	return &fakeIdempotencyStore{rows: map[string]fakeRow{}}
}

func (s *fakeIdempotencyStore) Check(ctx context.Context, profile string, key domain.IdempotencyKey) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[profile+"/"+key.Value]
	if !ok {
		return nil, false, nil
	}
	if !r.key.Matches(key) {
		return nil, false, domain.ErrIdempotencyConflict
	}
	return r.result, true, nil
}

func (s *fakeIdempotencyStore) Record(ctx context.Context, profile string, key domain.IdempotencyKey, result []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[profile+"/"+key.Value] = fakeRow{key: key, result: result, expires: expiresAt}
	return nil
}

func (s *fakeIdempotencyStore) Sweep(ctx context.Context, before time.Time) (int, error) {
	return 0, nil
}

// LoadOrStore satisfies the FR-034a extension to the IdempotencyStore
// port. These tests only exercise the 017 Check/Record path so the
// stub runs execute() directly and ignores the paramsHash.
func (s *fakeIdempotencyStore) LoadOrStore(ctx context.Context, _, _, key string, _ [32]byte, execute func() ([]byte, error)) ([]byte, error) {
	_ = key
	return execute()
}

type idempTestResult struct {
	MessageID string `json:"messageId"`
	TS        int64  `json:"ts"`
}

func TestIdempotentReplay(t *testing.T) {
	ctx := context.Background()
	store := newFakeIdempotencyStore()
	calls := 0
	fn := func(_ context.Context) (idempTestResult, error) {
		calls++
		return idempTestResult{MessageID: "m1", TS: 42}, nil
	}
	params := map[string]any{"to": "5511999999999@s.whatsapp.net", "body": "oi"}
	now := time.Unix(1_700_000_000, 0)

	r1, replayed1, err := IdempotencyExec(ctx, store, "default", "k-1", params, now, fn)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if replayed1 || calls != 1 || r1.MessageID != "m1" {
		t.Fatalf("first call shape wrong: replayed=%v calls=%d r=%+v", replayed1, calls, r1)
	}
	r2, replayed2, err := IdempotencyExec(ctx, store, "default", "k-1", params, now, fn)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !replayed2 || calls != 1 || r2.MessageID != r1.MessageID {
		t.Fatalf("replay shape wrong: replayed=%v calls=%d r=%+v", replayed2, calls, r2)
	}
}

func TestIdempotentConflict(t *testing.T) {
	ctx := context.Background()
	store := newFakeIdempotencyStore()
	fn := func(_ context.Context) (idempTestResult, error) {
		return idempTestResult{MessageID: "m1"}, nil
	}
	now := time.Unix(1_700_000_000, 0)

	_, _, err := IdempotencyExec(ctx, store, "default", "k-2",
		map[string]any{"body": "A"}, now, fn)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, _, err = IdempotencyExec(ctx, store, "default", "k-2",
		map[string]any{"body": "B"}, now, fn)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("conflict: want ErrIdempotencyConflict, got %v", err)
	}
}

func TestCanonicalJSONStripsReserved(t *testing.T) {
	a := map[string]any{"to": "x", "idempotencyKey": "k1", "requestId": "r1", "ts": 42}
	b := map[string]any{"to": "x"}
	ba, err := canonicalJSON(a)
	if err != nil {
		t.Fatalf("canonicalJSON a: %v", err)
	}
	bb, err := canonicalJSON(b)
	if err != nil {
		t.Fatalf("canonicalJSON b: %v", err)
	}
	if string(ba) != string(bb) {
		t.Fatalf("canonical mismatch:\n  a=%s\n  b=%s", ba, bb)
	}
	var decoded map[string]any
	if err := json.Unmarshal(ba, &decoded); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
}
