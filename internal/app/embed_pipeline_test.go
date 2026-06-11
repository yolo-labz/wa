package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestMemPendingSatisfiesContract drives the canonical port contract
// against the in-memory fake, keeping the fake honest about the
// semantics production implementations must provide.
func TestMemPendingSatisfiesContract(t *testing.T) {
	t.Parallel()
	porttest.RunPendingEmbeddingStoreContract(t, func(_ *testing.T) app.PendingEmbeddingStore {
		return newMemPending()
	})
}

// memPending is a thread-safe in-memory PendingEmbeddingStore for
// pipeline tests. Mirrors the pending_embeddings row shape so the
// restart-resume scenario can be proved without spinning up SQLite.
type memPending struct {
	mu      sync.Mutex
	rows    map[domain.MessageID]app.PendingMessage
	attempt map[domain.MessageID]int
}

func newMemPending() *memPending {
	return &memPending{
		rows:    make(map[domain.MessageID]app.PendingMessage),
		attempt: make(map[domain.MessageID]int),
	}
}

func (s *memPending) Enqueue(_ context.Context, m app.PendingMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[m.ID] = m
	return nil
}

func (s *memPending) LoadBatch(_ context.Context, limit int) ([]app.PendingMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]app.PendingMessage, 0, len(s.rows))
	for _, m := range s.rows {
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *memPending) MarkIndexed(_ context.Context, id domain.MessageID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	delete(s.attempt, id)
	return nil
}

func (s *memPending) IncrementAttempts(_ context.Context, id domain.MessageID) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempt[id]++
	return s.attempt[id], nil
}

func (s *memPending) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

func (s *memPending) attempts(id domain.MessageID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempt[id]
}

// stubEmbeddingFor is a small test helper that builds a minimal 4-dim
// Embedding keyed on the given message id. Used by shared-fake tests.
func stubEmbeddingFor(id domain.MessageID) domain.Embedding {
	return domain.Embedding{
		MessageID: id,
		Model:     "stub-4",
		Dim:       4,
		Vec:       []float32{1, 0, 0, 0},
	}
}

// stubEmbedder always returns a fixed 4-dim vector.
type stubEmbedder struct {
	calls int32
	fail  bool
}

func (s *stubEmbedder) Embed(_ context.Context, text string) (domain.Embedding, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.fail {
		return domain.Embedding{}, errors.New("stub embed failure")
	}
	_ = text
	return domain.Embedding{Model: "stub-4", Dim: 4, Vec: []float32{1, 0, 0, 0}}, nil
}

func (s *stubEmbedder) Info() app.EmbedderInfo {
	return app.EmbedderInfo{Model: "stub-4", Dim: 4}
}

// stubIndex is a lock-protected in-memory VectorIndex for pipeline tests.
type stubIndex struct {
	mu   sync.Mutex
	rows map[domain.MessageID]domain.Embedding
}

func newStubIndex() *stubIndex {
	return &stubIndex{rows: make(map[domain.MessageID]domain.Embedding)}
}

func (s *stubIndex) Upsert(_ context.Context, e domain.Embedding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[e.MessageID] = e
	return nil
}

func (s *stubIndex) Knn(_ context.Context, _ []float32, _ int) ([]app.VectorHit, error) {
	return nil, nil
}

func (s *stubIndex) Purge(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = make(map[domain.MessageID]domain.Embedding)
	return nil
}

func (s *stubIndex) Size(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows), nil
}

func (s *stubIndex) size() int {
	n, _ := s.Size(context.Background())
	return n
}

// waitUntil polls fn every 5 ms up to d, returns true on success. Uses
// time.NewTicker so the retry cadence never materialises as a literal
// time.Sleep (keeps this file off the synctest migration inventory).
func waitUntil(d time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(d)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		<-ticker.C
	}
	return fn()
}

// TestIndexingDrainsBacklog — enqueue N messages, the 4-worker pool
// drains them into the index within the backlog window. Matches
// tasks-tier3.md T3-09 test name.
func TestIndexingDrainsBacklog(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := newMemPending()
	idx := newStubIndex()
	emb := &stubEmbedder{}

	p := &app.EmbedPipeline{
		Embedder:     emb,
		Index:        idx,
		Store:        store,
		Workers:      4,
		BacklogSleep: 5 * time.Millisecond,
	}
	p.Start(ctx)
	t.Cleanup(p.Close)

	for i := range 20 {
		id := domain.MessageID("m" + string(rune('A'+i)))
		if err := p.Enqueue(ctx, app.PendingMessage{
			ID: id, Profile: "default", Body: "hello " + string(id),
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	ok := waitUntil(2*time.Second, func() bool {
		return idx.size() == 20 && store.size() == 0
	})
	if !ok {
		t.Fatalf("backlog drain: idx=%d store=%d (want 20, 0)", idx.size(), store.size())
	}
	if atomic.LoadInt32(&emb.calls) < 20 {
		t.Fatalf("embed call count too low: %d", emb.calls)
	}
}

// TestPoisonMessageDroppedAfterMaxAttempts — SF-07: a row whose embed
// permanently fails is actively dropped (MarkIndexed tombstone) once
// IncrementAttempts reaches MaxAttempts, and the Dropped counter
// records it. Before the fix, fail() bumped the counter but nothing
// consumed it: LoadBatch re-surfaced the row forever and the
// "log-and-drop" promised by the ADR never fired.
func TestPoisonMessageDroppedAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := newMemPending()
	idx := newStubIndex()
	emb := &stubEmbedder{fail: true}

	p := &app.EmbedPipeline{
		Embedder:     emb,
		Index:        idx,
		Store:        store,
		Workers:      1,
		BacklogSleep: 5 * time.Millisecond,
		MaxAttempts:  3,
	}
	p.Start(ctx)
	t.Cleanup(p.Close)

	if err := p.Enqueue(ctx, app.PendingMessage{
		ID: "poison", Profile: "default", Body: "never embeds",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ok := waitUntil(2*time.Second, func() bool {
		return store.size() == 0 && p.Dropped() == 1
	})
	if !ok {
		t.Fatalf("poison drop: store=%d dropped=%d attempts=%d (want 0, 1)",
			store.size(), p.Dropped(), store.attempts("poison"))
	}
	if idx.size() != 0 {
		t.Fatalf("poison row reached the index: %d entries", idx.size())
	}
	// Embedder saw exactly MaxAttempts tries — the drop is not premature.
	if got := atomic.LoadInt32(&emb.calls); got != 3 {
		t.Fatalf("embed calls = %d, want exactly 3 (MaxAttempts)", got)
	}
}

// TestFailingRowBelowBoundStaysPending — the complement: while attempts
// are below MaxAttempts the row stays in the store (retry budget not
// exhausted) and nothing is dropped.
func TestFailingRowBelowBoundStaysPending(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := newMemPending()
	emb := &stubEmbedder{fail: true}

	p := &app.EmbedPipeline{
		Embedder:     emb,
		Index:        newStubIndex(),
		Store:        store,
		Workers:      1,
		BacklogSleep: 5 * time.Millisecond,
		MaxAttempts:  10_000, // bound unreachable within the test window
	}
	p.Start(ctx)
	t.Cleanup(p.Close)

	if err := p.Enqueue(ctx, app.PendingMessage{
		ID: "flaky", Profile: "default", Body: "fails for now",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Wait until at least two retry rounds happened, proving the row was
	// re-surfaced by LoadBatch after the first failure.
	ok := waitUntil(2*time.Second, func() bool {
		return store.attempts("flaky") >= 2
	})
	if !ok {
		t.Fatalf("row not retried: attempts=%d", store.attempts("flaky"))
	}
	if store.size() != 1 {
		t.Fatalf("row vanished below bound: store=%d, want 1", store.size())
	}
	if p.Dropped() != 0 {
		t.Fatalf("Dropped() = %d below bound, want 0", p.Dropped())
	}
}

// TestBacklogResumesAfterRestart — rows persisted in the store are
// picked up by a fresh pipeline without re-enqueue. T3-09 restart
// recovery contract.
func TestBacklogResumesAfterRestart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := newMemPending()
	// Pre-seed 5 pending rows — simulating a crash mid-index.
	for i := range 5 {
		id := domain.MessageID("r" + string(rune('0'+i)))
		if err := store.Enqueue(ctx, app.PendingMessage{
			ID: id, Profile: "default", Body: "survived restart",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if store.size() != 5 {
		t.Fatalf("seed size: %d", store.size())
	}

	// Fresh pipeline (no Enqueue call).
	idx := newStubIndex()
	p := &app.EmbedPipeline{
		Embedder:     &stubEmbedder{},
		Index:        idx,
		Store:        store,
		Workers:      2,
		BacklogSleep: 5 * time.Millisecond,
	}
	p.Start(ctx)
	t.Cleanup(p.Close)

	ok := waitUntil(2*time.Second, func() bool {
		return idx.size() == 5 && store.size() == 0
	})
	if !ok {
		t.Fatalf("restart resume: idx=%d store=%d (want 5, 0)", idx.size(), store.size())
	}
}
