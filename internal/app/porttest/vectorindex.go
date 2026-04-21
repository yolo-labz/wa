package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// RunVectorIndexContract exercises the FR-101 VectorIndex contract
// (VI1..VI4). The factory is handed the dim the suite will use.
func RunVectorIndexContract(t *testing.T, dim int, factory func(t *testing.T) app.VectorIndex) {
	t.Helper()

	oneHot := func(i int) []float32 {
		v := make([]float32, dim)
		v[i%dim] = 1
		return v
	}
	emb := func(id string, i int) domain.Embedding {
		return domain.Embedding{
			MessageID: domain.MessageID(id),
			Model:     "test",
			Dim:       dim,
			Vec:       oneHot(i),
		}
	}

	t.Run("VI1_upsert_then_knn", func(t *testing.T) {
		idx := factory(t)
		for i := 0; i < 3; i++ {
			if err := idx.Upsert(context.Background(), emb("m-"+string(rune('a'+i)), i)); err != nil {
				reportf(t, "VectorIndex", "Upsert", "VI1", "nil err", err.Error())
			}
		}
		hits, err := idx.Knn(context.Background(), oneHot(1), 2)
		if err != nil {
			reportf(t, "VectorIndex", "Knn", "VI1", "nil err", err.Error())
			return
		}
		if len(hits) == 0 || hits[0].MessageID != domain.MessageID("m-b") {
			reportf(t, "VectorIndex", "Knn", "VI1", "nearest = m-b", "mismatch")
		}
	})

	t.Run("VI2_upsert_replaces", func(t *testing.T) {
		idx := factory(t)
		_ = idx.Upsert(context.Background(), emb("m-x", 0))
		_ = idx.Upsert(context.Background(), emb("m-x", 2))
		n, err := idx.Size(context.Background())
		if err != nil {
			reportf(t, "VectorIndex", "Size", "VI2", "nil err", err.Error())
		}
		if n != 1 {
			reportf(t, "VectorIndex", "Size", "VI2", "size == 1 after replace", "n != 1")
		}
	})

	t.Run("VI3_purge_empties", func(t *testing.T) {
		idx := factory(t)
		_ = idx.Upsert(context.Background(), emb("m-y", 0))
		if err := idx.Purge(context.Background()); err != nil {
			reportf(t, "VectorIndex", "Purge", "VI3", "nil err", err.Error())
		}
		n, _ := idx.Size(context.Background())
		if n != 0 {
			reportf(t, "VectorIndex", "Size", "VI3", "size == 0 after purge", "non-zero")
		}
	})

	t.Run("VI4_knn_k_bounded", func(t *testing.T) {
		idx := factory(t)
		for i := 0; i < 5; i++ {
			_ = idx.Upsert(context.Background(), emb(string(rune('a'+i)), i))
		}
		hits, _ := idx.Knn(context.Background(), oneHot(0), 3)
		if len(hits) > 3 {
			reportf(t, "VectorIndex", "Knn", "VI4", "len(hits) ≤ k", "len > 3")
		}
	})
}
