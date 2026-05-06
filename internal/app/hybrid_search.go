package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// rrfK is the Reciprocal Rank Fusion constant recommended by Cormack,
// Clarke & Büttcher (SIGIR 2009). k=60 is the widely-replicated sweet
// spot that dampens the top-rank advantage enough to let each retriever
// contribute without being dominated by the other. FR-101 / SC-09.
const rrfK = 60

// HybridSearcher is the FR-101 use case that fuses BM25 with dense
// vector retrieval under Reciprocal Rank Fusion. It owns no state; all
// dependencies are injected so the composition root can swap adapters
// (e.g. memory index in tests, sqlite-vec in prod) without touching the
// fusion logic.
//
// Nil instance is valid and behaves as "hybrid unavailable" — callers
// MUST fall back to BM25 plus the user-visible hint at the dispatch
// layer before reaching this type.
type HybridSearcher struct {
	Searcher MessageSearcher
	Embedder Embedder
	Index    VectorIndex
	// CandidateMultiplier sets how many more candidates to pull from
	// each retriever than the caller's final limit. The pool is fused
	// then trimmed to limit. Defaults to 3 when zero. Higher values
	// increase recall at the cost of embedder latency.
	CandidateMultiplier int
}

// candidateMultiplier returns the effective pool-size multiplier.
func (h *HybridSearcher) candidateMultiplier() int {
	if h.CandidateMultiplier <= 0 {
		return 3
	}
	return h.CandidateMultiplier
}

// Search runs BM25 + dense KNN and fuses their rankings via RRF. It
// returns up to limit hits ordered by fused score desc.
//
// On Embedder or VectorIndex failure it degrades silently to BM25-only
// and returns the BM25 hits. Callers that need strict hybrid-or-fail
// should check err from the dependency directly.
func (h *HybridSearcher) Search(ctx context.Context, q SearchQuery, limit int) ([]SearchHit, error) {
	if h == nil || h.Searcher == nil {
		return nil, errors.New("hybrid search: no searcher wired")
	}
	if limit <= 0 {
		limit = 20
	}
	pool := limit * h.candidateMultiplier()
	bm, err := h.Searcher.SearchBM25(ctx, q, pool)
	if err != nil {
		return nil, fmt.Errorf("hybrid search: bm25: %w", err)
	}
	if h.Embedder == nil || h.Index == nil {
		return trim(bm, limit), nil
	}
	text := queryText(q)
	if text == "" {
		return trim(bm, limit), nil
	}
	emb, err := h.Embedder.Embed(ctx, text)
	if err != nil {
		return trim(bm, limit), nil
	}
	vec, err := h.Index.Knn(ctx, emb.Vec, pool)
	if err != nil {
		return trim(bm, limit), nil
	}
	return FuseRRF(bm, vec, rrfK, limit), nil
}

// FuseRRF combines two ranked retrievers into one list using the
// Reciprocal Rank Fusion score s(d) = Σ_r 1 / (k + rank_r(d)). Documents
// appearing in only one list contribute only that list's term. Metadata
// (chat, sender, snippet, ts) is taken from the BM25 hit when available
// so the returned SearchHit is renderable; vector-only hits carry just
// the MessageID (consumer is responsible for re-hydrating metadata if
// it needs to render them to the user).
//
// Rank numbers are 1-based to match the canonical RRF formulation.
func FuseRRF(bm []SearchHit, vec []VectorHit, k int, limit int) []SearchHit {
	if k <= 0 {
		k = rrfK
	}
	scores := make(map[domain.MessageID]float64, len(bm)+len(vec))
	meta := make(map[domain.MessageID]SearchHit, len(bm))

	for i, h := range bm {
		rank := i + 1
		scores[h.MessageID] += 1.0 / float64(k+rank)
		meta[h.MessageID] = h
	}
	for i, v := range vec {
		rank := i + 1
		scores[v.MessageID] += 1.0 / float64(k+rank)
	}

	out := make([]SearchHit, 0, len(scores))
	for id, s := range scores {
		if h, ok := meta[id]; ok {
			h.Score = s
			out = append(out, h)
			continue
		}
		out = append(out, SearchHit{MessageID: id, Score: s})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].MessageID < out[j].MessageID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// queryText collapses a SearchQuery into a single string for embedding.
// Phrase wins; All+Any terms are space-joined as a fallback so the
// embedder sees the user's intent even when the caller used the
// structured-term API.
func queryText(q SearchQuery) string {
	if s := strings.TrimSpace(q.Phrase); s != "" {
		return s
	}
	parts := make([]string, 0, len(q.All)+len(q.Any))
	parts = append(parts, q.All...)
	parts = append(parts, q.Any...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func trim(hits []SearchHit, limit int) []SearchHit {
	if limit > 0 && len(hits) > limit {
		return hits[:limit]
	}
	return hits
}
