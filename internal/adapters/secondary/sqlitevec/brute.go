// Package sqlitevec holds the pure-Go VectorIndex fallback (FR-101, T3-08).
// The full sqlite-vec/WASM adapter lives behind a build tag and is wired
// later (T3-07); until then Brute is the reference implementation the
// contract test suite runs against.
package sqlitevec

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// BruteMaxRows is the soft ceiling above which callers SHOULD prefer a
// real ANN index. At 384-dim × 10 000 rows the working set is ~3.7 MB and
// scan latency stays well under 10 ms on Apple Silicon.
const BruteMaxRows = 10_000

// ErrDimMismatch is returned when an Upsert or Knn vector's length
// disagrees with the index's configured Dim.
var ErrDimMismatch = errors.New("sqlitevec/brute: dim mismatch")

// Brute is an in-memory, pure-Go VectorIndex. Vectors are INT8 quantised
// per-row (scale = max_abs/127) on insert; search performs a linear scan
// with dequantisation and cosine normalisation. Thread-safe via RWMutex.
type Brute struct {
	dim int

	mu   sync.RWMutex
	rows map[domain.MessageID]bruteRow
}

type bruteRow struct {
	q     []int8
	scale float32 // max_abs / 127 — per-row dequantisation factor
	norm  float32 // L2 norm of dequantised vector; cosine denominator
}

// NewBrute constructs an empty index of the given dim. Dim must be > 0.
func NewBrute(dim int) (*Brute, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("sqlitevec/brute: dim must be positive, got %d", dim)
	}
	return &Brute{
		dim:  dim,
		rows: make(map[domain.MessageID]bruteRow),
	}, nil
}

// Upsert stores or replaces the vector for e.MessageID.
func (b *Brute) Upsert(ctx context.Context, e domain.Embedding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.Dim != b.dim || len(e.Vec) != b.dim {
		return ErrDimMismatch
	}
	if e.MessageID == "" {
		return fmt.Errorf("sqlitevec/brute: MessageID required")
	}
	q, scale, n := quantise(e.Vec)

	b.mu.Lock()
	b.rows[e.MessageID] = bruteRow{q: q, scale: scale, norm: n}
	b.mu.Unlock()
	return nil
}

// Knn returns the top-k nearest neighbours by cosine similarity.
func (b *Brute) Knn(ctx context.Context, query []float32, k int) ([]app.VectorHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(query) != b.dim {
		return nil, ErrDimMismatch
	}
	if k <= 0 {
		return nil, nil
	}
	qNorm := norm(query)
	if qNorm == 0 {
		return nil, nil
	}

	b.mu.RLock()
	hits := make([]app.VectorHit, 0, len(b.rows))
	for id, row := range b.rows {
		if row.norm == 0 {
			continue
		}
		var dot float32
		for i, qi := range row.q {
			dot += float32(qi) * row.scale * query[i]
		}
		sim := dot / (row.norm * qNorm)
		hits = append(hits, app.VectorHit{MessageID: id, Score: sim})
	}
	b.mu.RUnlock()

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Purge drops every vector.
func (b *Brute) Purge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	b.rows = make(map[domain.MessageID]bruteRow)
	b.mu.Unlock()
	return nil
}

// Size returns the count of stored vectors.
func (b *Brute) Size(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	b.mu.RLock()
	n := len(b.rows)
	b.mu.RUnlock()
	return n, nil
}

// quantise converts float32→int8 using a per-row max-abs-normalised
// scheme. Returns (q, scale, norm) where:
//
//   - q[i] = round(v[i]/max_abs * 127)  (∈ [-127, 127])
//   - scale = max_abs / 127             (dequantisation factor)
//   - norm = L2 norm of the dequantised vector (for cosine denominator)
//
// Dequantised vector is approximately v (within ±max_abs/127 per-element).
func quantise(v []float32) ([]int8, float32, float32) {
	q := make([]int8, len(v))
	var maxAbs float32
	for _, x := range v {
		a := abs32(x)
		if a > maxAbs {
			maxAbs = a
		}
	}
	if maxAbs == 0 {
		return q, 0, 0
	}
	scale := maxAbs / 127
	for i, x := range v {
		r := math.Round(float64(x / maxAbs * 127))
		if r > 127 {
			r = 127
		} else if r < -127 {
			r = -127
		}
		q[i] = int8(r)
	}
	// Dequantised norm ≈ ||v||; good enough for cosine at INT8 precision.
	var sq float32
	for _, qi := range q {
		f := float32(qi) * scale
		sq += f * f
	}
	return q, scale, float32(math.Sqrt(float64(sq)))
}

func norm(v []float32) float32 {
	var sq float32
	for _, x := range v {
		sq += x * x
	}
	return float32(math.Sqrt(float64(sq)))
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
