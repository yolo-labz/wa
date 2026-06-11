package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// PendingEmbeddingStoreFactory returns a fresh PendingEmbeddingStore
// for one sub-test.
type PendingEmbeddingStoreFactory func(t *testing.T) app.PendingEmbeddingStore

// RunPendingEmbeddingStoreContract runs every contract clause for the
// PendingEmbeddingStore port (FR-103 durable embed backlog). Clauses:
//
//	PE1 Enqueue then LoadBatch round-trips the row fields.
//	PE2 LoadBatch honours the limit argument.
//	PE3 MarkIndexed removes the row from subsequent LoadBatch calls.
//	PE4 IncrementAttempts returns the post-increment count,
//	    monotonically increasing per id.
//	PE5 MarkIndexed clears the attempt counter — a re-Enqueue of the
//	    same id starts with a fresh retry budget (poison-drop tombstone
//	    must not poison a later legitimate retry, SF-07).
func RunPendingEmbeddingStoreContract(t *testing.T, factory PendingEmbeddingStoreFactory) {
	t.Helper()
	ctx := context.Background()

	msg := app.PendingMessage{
		ID:      domain.MessageID("wa_pe1"),
		Profile: "default",
		Body:    "embed me",
	}

	t.Run("PE1 Enqueue round-trips via LoadBatch", func(t *testing.T) {
		s := factory(t)
		if err := s.Enqueue(ctx, msg); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		batch, err := s.LoadBatch(ctx, 10)
		if err != nil {
			t.Fatalf("LoadBatch: %v", err)
		}
		if len(batch) != 1 {
			reportf(t, "PendingEmbeddingStore", "LoadBatch", "PE1", "1 row", itoa(len(batch))+" rows")
			return
		}
		got := batch[0]
		if got.ID != msg.ID || got.Profile != msg.Profile || got.Body != msg.Body {
			reportf(t, "PendingEmbeddingStore", "LoadBatch", "PE1",
				"fields preserved", string(got.ID)+"/"+got.Profile+"/"+got.Body)
		}
	})

	t.Run("PE2 LoadBatch honours limit", func(t *testing.T) {
		s := factory(t)
		for i := range 5 {
			m := msg
			m.ID = domain.MessageID("wa_pe2_" + itoa(i))
			if err := s.Enqueue(ctx, m); err != nil {
				t.Fatalf("Enqueue %d: %v", i, err)
			}
		}
		batch, err := s.LoadBatch(ctx, 3)
		if err != nil {
			t.Fatalf("LoadBatch: %v", err)
		}
		if len(batch) > 3 {
			reportf(t, "PendingEmbeddingStore", "LoadBatch", "PE2", "≤3 rows", itoa(len(batch))+" rows")
		}
	})

	t.Run("PE3 MarkIndexed removes row", func(t *testing.T) {
		s := factory(t)
		if err := s.Enqueue(ctx, msg); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if err := s.MarkIndexed(ctx, msg.ID); err != nil {
			t.Fatalf("MarkIndexed: %v", err)
		}
		batch, err := s.LoadBatch(ctx, 10)
		if err != nil {
			t.Fatalf("LoadBatch: %v", err)
		}
		if len(batch) != 0 {
			reportf(t, "PendingEmbeddingStore", "MarkIndexed", "PE3", "0 rows after mark", itoa(len(batch))+" rows")
		}
	})

	t.Run("PE4 IncrementAttempts post-increment monotonic", func(t *testing.T) {
		s := factory(t)
		if err := s.Enqueue(ctx, msg); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		for want := 1; want <= 3; want++ {
			got, err := s.IncrementAttempts(ctx, msg.ID)
			if err != nil {
				t.Fatalf("IncrementAttempts #%d: %v", want, err)
			}
			if got != want {
				reportf(t, "PendingEmbeddingStore", "IncrementAttempts", "PE4",
					"count="+itoa(want), "count="+itoa(got))
			}
		}
	})

	t.Run("PE5 MarkIndexed resets attempt counter", func(t *testing.T) {
		s := factory(t)
		if err := s.Enqueue(ctx, msg); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if _, err := s.IncrementAttempts(ctx, msg.ID); err != nil {
			t.Fatalf("IncrementAttempts: %v", err)
		}
		if err := s.MarkIndexed(ctx, msg.ID); err != nil {
			t.Fatalf("MarkIndexed: %v", err)
		}
		if err := s.Enqueue(ctx, msg); err != nil {
			t.Fatalf("re-Enqueue: %v", err)
		}
		got, err := s.IncrementAttempts(ctx, msg.ID)
		if err != nil {
			t.Fatalf("IncrementAttempts after re-enqueue: %v", err)
		}
		if got != 1 {
			reportf(t, "PendingEmbeddingStore", "IncrementAttempts", "PE5",
				"count=1 (fresh budget)", "count="+itoa(got))
		}
	})
}
