package app_test

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// --- Stub Embedder + VectorIndex --------------------------------------
//
// Deterministic, 4-dimensional semantic space keyed on "topic" label.
// Every doc carries a topic; the embedder maps the doc's body text to
// that topic's canonical vector, and the query is mapped the same way.
// KNN is cosine similarity over the docs we've Upserted. This gives a
// reproducible test of RRF fusion: BM25 only matches literal keywords;
// the embedder catches topical synonyms BM25 misses.

type topicEmbedder struct {
	topics map[string][]float32
}

func (e *topicEmbedder) Info() app.EmbedderInfo {
	return app.EmbedderInfo{Model: "stub-topic-4", Dim: 4}
}

func (e *topicEmbedder) Embed(_ context.Context, text string) (domain.Embedding, error) {
	vec := e.vectorFor(text)
	return domain.Embedding{
		Model: "stub-topic-4",
		Dim:   4,
		Vec:   vec,
	}, nil
}

// vectorFor maps a body/query to the best-matching topic vector. Match
// is first-hit substring over the topic's keyword list; unknown text
// falls into an "unknown" orthogonal direction.
func (e *topicEmbedder) vectorFor(text string) []float32 {
	t := strings.ToLower(text)
	for topic, vec := range e.topics {
		for _, kw := range topicKeywords[topic] {
			if strings.Contains(t, kw) {
				return vec
			}
		}
	}
	return []float32{0, 0, 0, 1}
}

// topicKeywords is a deliberately lossy "semantic index" — the refund
// cluster shares synonyms BM25 can't match on the single token "refund".
var topicKeywords = map[string][]string{
	"refund":   {"refund", "money back", "reimburs", "chargeback", "return my payment"},
	"login":    {"login", "password", "sign in", "credential"},
	"feature":  {"feature", "roadmap", "enhancement"},
	"greeting": {"hi", "hello", "howdy"},
}

// memIndex is a tiny in-memory cosine KNN brute-force index.
type memIndex struct {
	rows []memRow
}

type memRow struct {
	id  domain.MessageID
	vec []float32
}

func (m *memIndex) Upsert(_ context.Context, e domain.Embedding) error {
	for i, r := range m.rows {
		if r.id == e.MessageID {
			m.rows[i].vec = e.Vec
			return nil
		}
	}
	m.rows = append(m.rows, memRow{id: e.MessageID, vec: e.Vec})
	return nil
}

func (m *memIndex) Knn(_ context.Context, q []float32, k int) ([]app.VectorHit, error) {
	type scored struct {
		id    domain.MessageID
		score float32
	}
	out := make([]scored, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, scored{r.id, cosine(q, r.vec)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	hits := make([]app.VectorHit, len(out))
	for i, s := range out {
		hits[i] = app.VectorHit{MessageID: s.id, Score: s.score}
	}
	return hits, nil
}

func (m *memIndex) Purge(_ context.Context) error       { m.rows = nil; return nil }
func (m *memIndex) Size(_ context.Context) (int, error) { return len(m.rows), nil }

func cosine(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// --- Corpus + stub BM25 searcher -------------------------------------

type corpusDoc struct {
	id    domain.MessageID
	body  string
	topic string
}

var synthCorpus = []corpusDoc{
	// Refund topic: 5 docs total.
	{"m01", "How do I get a refund for my order?", "refund"},
	{"m02", "The customer asked for a refund yesterday.", "refund"},
	{"m03", "Refund policy requires a receipt.", "refund"},
	// Synonyms BM25 misses on token "refund":
	{"m04", "Can I get my money back on the purchase?", "refund"},
	{"m05", "Requesting a reimbursement for travel costs.", "refund"},

	// Unrelated clutter, 15 docs.
	{"m06", "Forgot my password again, need help signing in.", "login"},
	{"m07", "Password reset email never arrived.", "login"},
	{"m08", "Sign in page throws a 500 error.", "login"},
	{"m09", "New credential flow is live tomorrow.", "login"},
	{"m10", "Login screen shows wrong logo.", "login"},
	{"m11", "Feature request: dark mode toggle.", "feature"},
	{"m12", "Roadmap for Q3 includes SSO.", "feature"},
	{"m13", "Enhancement proposal on the tracker.", "feature"},
	{"m14", "Feature flag rollout plan drafted.", "feature"},
	{"m15", "Feature deprecation scheduled.", "feature"},
	{"m16", "Hi team, quick sync at 10?", "greeting"},
	{"m17", "Hello everyone, welcome aboard!", "greeting"},
	{"m18", "Howdy, long time no see.", "greeting"},
	{"m19", "Hello from the new hire batch.", "greeting"},
	{"m20", "Hi all, the deck is shared.", "greeting"},
}

// bm25Stub is a deliberately literal BM25 — token-intersection over the
// query phrase, ranked by match count then by doc order. This mirrors
// real FTS5 tokenisation for a short query and is crucially myopic: it
// can't match "money back" when the query is "refund".
type bm25Stub struct{ docs []corpusDoc }

func (b *bm25Stub) SearchBM25(_ context.Context, q app.SearchQuery, limit int) ([]app.SearchHit, error) {
	term := strings.ToLower(strings.TrimSpace(q.Phrase))
	if term == "" {
		return nil, nil
	}
	type scored struct {
		doc   corpusDoc
		score float64
	}
	out := make([]scored, 0, len(b.docs))
	for _, d := range b.docs {
		if strings.Contains(strings.ToLower(d.body), term) {
			out = append(out, scored{d, 1.0})
		}
	}
	// Stable order: match score then doc index.
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	hits := make([]app.SearchHit, len(out))
	for i, s := range out {
		hits[i] = app.SearchHit{
			MessageID: s.doc.id,
			Snippet:   s.doc.body,
			TS:        time.Unix(1_700_000_000+int64(i), 0),
			Score:     s.score,
		}
	}
	return hits, nil
}

// --- Recall helper ---------------------------------------------------

func recallAtK(hits []app.SearchHit, relevant map[domain.MessageID]bool, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	limit := k
	if limit > len(hits) {
		limit = len(hits)
	}
	seen := 0
	for i := 0; i < limit; i++ {
		if relevant[hits[i].MessageID] {
			seen++
		}
	}
	return float64(seen) / float64(len(relevant))
}

// TestRrfFusionRecallUplift is the SC-09 gate: hybrid RRF must lift
// recall@5 by at least 0.15 over BM25 alone on the synonym-heavy
// synthetic corpus. Matches tasks-tier3.md row T3-11.
func TestRrfFusionRecallUplift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Ground truth: the refund-topic doc set.
	relevant := map[domain.MessageID]bool{
		"m01": true, "m02": true, "m03": true, "m04": true, "m05": true,
	}

	// Topic embedding space: refund cluster sits at (1,0,0,0), others
	// orthogonal, so cosine is 1 for refund docs and ~0 for the rest.
	embedder := &topicEmbedder{topics: map[string][]float32{
		"refund":   {1, 0, 0, 0},
		"login":    {0, 1, 0, 0},
		"feature":  {0, 0, 1, 0},
		"greeting": {0, 0, 0, 1},
	}}

	// Index every doc.
	idx := &memIndex{}
	for _, d := range synthCorpus {
		vec := embedder.vectorFor(d.body)
		if err := idx.Upsert(ctx, domain.Embedding{
			MessageID: d.id,
			Model:     "stub-topic-4",
			Dim:       4,
			Vec:       vec,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	searcher := &bm25Stub{docs: synthCorpus}
	hybrid := &app.HybridSearcher{
		Searcher: searcher,
		Embedder: embedder,
		Index:    idx,
	}

	// --- Baseline: BM25 only ---------------------------------------
	bmHits, err := searcher.SearchBM25(ctx, app.SearchQuery{Phrase: "refund"}, 5)
	if err != nil {
		t.Fatalf("bm25: %v", err)
	}
	bmRecall := recallAtK(bmHits, relevant, 5)

	// --- Hybrid: RRF -----------------------------------------------
	hybridHits, err := hybrid.Search(ctx, app.SearchQuery{Phrase: "refund"}, 5)
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	hybridRecall := recallAtK(hybridHits, relevant, 5)

	uplift := hybridRecall - bmRecall
	if uplift < 0.15 {
		t.Fatalf("SC-09: hybrid recall uplift %.3f below 0.15 gate (bm25=%.3f, hybrid=%.3f)",
			uplift, bmRecall, hybridRecall)
	}
	t.Logf("recall@5 bm25=%.3f hybrid=%.3f uplift=%.3f", bmRecall, hybridRecall, uplift)
}

// TestFuseRRFEmptyInputs — fusion returns an empty slice when both
// retrievers return nothing.
func TestFuseRRFEmptyInputs(t *testing.T) {
	t.Parallel()
	out := app.FuseRRF(nil, nil, 60, 10)
	if len(out) != 0 {
		t.Fatalf("expected empty fusion, got %d", len(out))
	}
}

// TestFuseRRFBM25OnlyPreservesOrder — when only BM25 contributes, the
// fusion order matches the BM25 rank (ties broken by message id).
func TestFuseRRFBM25OnlyPreservesOrder(t *testing.T) {
	t.Parallel()
	bm := []app.SearchHit{
		{MessageID: "a"}, {MessageID: "b"}, {MessageID: "c"},
	}
	out := app.FuseRRF(bm, nil, 60, 3)
	want := []domain.MessageID{"a", "b", "c"}
	for i, h := range out {
		if h.MessageID != want[i] {
			t.Fatalf("order[%d]: got %s want %s", i, h.MessageID, want[i])
		}
	}
}

// TestFuseRRFBothListsLiftOverlap — a doc appearing in both lists
// outranks docs appearing in only one, even if their individual ranks
// are higher.
func TestFuseRRFBothListsLiftOverlap(t *testing.T) {
	t.Parallel()
	bm := []app.SearchHit{{MessageID: "x"}, {MessageID: "y"}}  // y at rank 2
	vec := []app.VectorHit{{MessageID: "y"}, {MessageID: "z"}} // y at rank 1
	out := app.FuseRRF(bm, vec, 60, 3)
	if out[0].MessageID != "y" {
		t.Fatalf("overlap should win: got %s", out[0].MessageID)
	}
}
