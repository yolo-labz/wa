package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/yolo-labz/wa/internal/app"
)

// EventBuffer is the in-memory drop-oldest ring-buffer implementation of
// app.EventBuffer. Capacity defaults to 10_000 per FR-062.
type EventBuffer struct {
	mu           sync.Mutex
	ring         []app.EventRecord
	capacity     int
	nextSeq      int64
	oldestSeq    int64
	newestSeq    int64
	droppedTotal int64
}

// NewEventBuffer returns a fresh EventBuffer with the given capacity.
// capacity <= 0 falls back to 10_000.
func NewEventBuffer(capacity int) *EventBuffer {
	if capacity <= 0 {
		capacity = 10_000
	}
	return &EventBuffer{
		ring:     make([]app.EventRecord, 0, capacity),
		capacity: capacity,
	}
}

// Append implements app.EventBuffer. Assigns a monotonic seq if the input
// record has seq == 0; otherwise respects the caller-supplied seq (useful
// for replay).
func (b *EventBuffer) Append(ctx context.Context, rec app.EventRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec.Seq == 0 {
		b.nextSeq++
		rec.Seq = b.nextSeq
	} else if rec.Seq > b.nextSeq {
		b.nextSeq = rec.Seq
	}
	if len(b.ring) >= b.capacity {
		b.ring = b.ring[1:]
		b.droppedTotal++
	}
	b.ring = append(b.ring, rec)
	b.newestSeq = rec.Seq
	b.oldestSeq = b.ring[0].Seq
	return nil
}

// Range implements app.EventBuffer.
func (b *EventBuffer) Range(ctx context.Context, sinceSeq int64, limit int) ([]app.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit %d", ErrInvalidLimit, limit)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]app.EventRecord, 0, limit)
	for _, r := range b.ring {
		if r.Seq <= sinceSeq {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Stats implements app.EventBuffer.
func (b *EventBuffer) Stats(ctx context.Context) (app.BufferStats, error) {
	if err := ctx.Err(); err != nil {
		return app.BufferStats{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return app.BufferStats{
		Size:         len(b.ring),
		Capacity:     b.capacity,
		OldestSeq:    b.oldestSeq,
		NewestSeq:    b.newestSeq,
		DroppedTotal: b.droppedTotal,
	}, nil
}

// TrimTo implements app.EventBuffer. Removes entries with seq <= arg.
func (b *EventBuffer) TrimTo(ctx context.Context, seq int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cut := 0
	for _, r := range b.ring {
		if r.Seq <= seq {
			cut++
		} else {
			break
		}
	}
	b.ring = b.ring[cut:]
	if len(b.ring) > 0 {
		b.oldestSeq = b.ring[0].Seq
	} else {
		b.oldestSeq = 0
	}
	return cut, nil
}
