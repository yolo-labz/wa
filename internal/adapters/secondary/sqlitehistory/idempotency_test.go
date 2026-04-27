package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
	_ "modernc.org/sqlite"
)

func openV4SidecarDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := migrateIfNeeded(ctx, db, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestLoadOrStoreReplayBytesIdentical(t *testing.T) {
	t.Parallel()
	db := openV4SidecarDB(t)
	s := NewIdempotencySidecar(db, nil)
	ctx := context.Background()
	var hash [32]byte
	copy(hash[:], []byte("0123456789abcdef0123456789abcdef"))
	payload := []byte(`{"messageId":"M1","timestamp":123}`)
	got1, err := s.LoadOrStore(ctx, "send", "default", "k1", hash, func() ([]byte, error) {
		return payload, nil
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if string(got1) != string(payload) {
		t.Fatalf("first got %q want %q", got1, payload)
	}
	var executed int32
	got2, err := s.LoadOrStore(ctx, "send", "default", "k1", hash, func() ([]byte, error) {
		atomic.AddInt32(&executed, 1)
		return []byte(`{"ignored":true}`), nil
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(got2) != string(payload) {
		t.Fatalf("second got %q want %q (replay must be byte-identical)", got2, payload)
	}
	if atomic.LoadInt32(&executed) != 0 {
		t.Fatal("execute callback must not fire on replay")
	}
}

func TestLoadOrStoreCollisionReturnsDomainError(t *testing.T) {
	t.Parallel()
	db := openV4SidecarDB(t)
	s := NewIdempotencySidecar(db, nil)
	ctx := context.Background()
	var h1, h2 [32]byte
	copy(h1[:], []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	copy(h2[:], []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	if _, err := s.LoadOrStore(ctx, "send", "default", "k1", h1, func() ([]byte, error) {
		return []byte(`ok`), nil
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := s.LoadOrStore(ctx, "send", "default", "k1", h2, func() ([]byte, error) {
		t.Fatal("execute must not fire on collision")
		return nil, nil
	})
	if !errors.Is(err, domain.ErrIdempotencyCollision) {
		t.Fatalf("want ErrIdempotencyCollision, got %v", err)
	}
}

func TestLoadOrStoreEmptyKeyBypassesSidecar(t *testing.T) {
	t.Parallel()
	db := openV4SidecarDB(t)
	s := NewIdempotencySidecar(db, nil)
	ctx := context.Background()
	var fires int32
	var hash [32]byte
	for i := range 3 {
		_, err := s.LoadOrStore(ctx, "send", "default", "", hash, func() ([]byte, error) {
			atomic.AddInt32(&fires, 1)
			return []byte(`ok`), nil
		})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&fires); got != 3 {
		t.Fatalf("empty-key fires = %d want 3 (must bypass cache)", got)
	}
}

func TestSidecarTTLSweep5m(t *testing.T) {
	t.Parallel()
	db := openV4SidecarDB(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	clk := &atomicClock{}
	clk.set(now)
	s := NewIdempotencySidecar(db, clk.now)
	ctx := context.Background()
	var hash [32]byte
	if _, err := s.LoadOrStore(ctx, "send", "default", "k1", hash, func() ([]byte, error) {
		return []byte(`ok`), nil
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
	// Advance the clock past the 24h TTL and sweep.
	clk.set(now.Add(25 * time.Hour))
	n, err := s.Sweep(ctx, clk.now())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("sweep deleted %d want 1", n)
	}
	// A subsequent LoadOrStore MUST re-execute since cache was evicted.
	var fires int32
	if _, err := s.LoadOrStore(ctx, "send", "default", "k1", hash, func() ([]byte, error) {
		atomic.AddInt32(&fires, 1)
		return []byte(`ok`), nil
	}); err != nil {
		t.Fatalf("post-sweep store: %v", err)
	}
	if got := atomic.LoadInt32(&fires); got != 1 {
		t.Fatalf("post-sweep fires = %d want 1", got)
	}
}

// atomicClock is a tiny thread-safe clock for TTL tests.
type atomicClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *atomicClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *atomicClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
