package whatsmeow

import (
	"context"
	"sync"

	"github.com/yolo-labz/wa/internal/domain"
)

// auditRingBuffer is the in-memory audit log used by the whatsmeow adapter
// in v0. It satisfies app.AuditLog via the Record method. Feature 004 will
// swap this out for internal/adapters/secondary/slogaudit/ which writes to
// $XDG_STATE_HOME/wa/audit.log per CLAUDE.md §"Safety".
//
// The buffer is a fixed-capacity circular log: once full, each new Record
// overwrites the oldest entry. Silent wrap-around is the documented
// behaviour per data-model.md §"Audit ring buffer" — the point of the v0
// buffer is to surface recent activity for debugging, not to be a durable
// audit trail (slogaudit is).
type auditRingBuffer struct {
	mu   sync.Mutex
	buf  []domain.AuditEvent
	head int  // index of the next write position
	full bool // true once the buffer has wrapped at least once
	cap  int
}

// newAuditRing constructs an auditRingBuffer with the given capacity. A
// capacity of 1000 is the default chosen in data-model.md; tests may use
// smaller capacities to exercise wrap-around.
func newAuditRing(capacity int) *auditRingBuffer {
	if capacity <= 0 {
		panic("whatsmeow adapter: auditRing capacity must be > 0")
	}
	return &auditRingBuffer{
		buf: make([]domain.AuditEvent, capacity),
		cap: capacity,
	}
}

// Record appends an audit event to the ring, overwriting the oldest entry
// if the ring is already full. It satisfies app.AuditLog. The ctx argument
// is accepted for interface conformance but not consulted — the operation
// is a single in-memory write and never blocks.
func (r *auditRingBuffer) Record(_ context.Context, e domain.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = e
	r.head++
	if r.head >= r.cap {
		r.head = 0
		r.full = true
	}
	return nil
}

// Snapshot returns a defensive copy of the current buffer contents in
// insertion order (oldest first). Tests and the future `wa audit tail`
// subcommand read from here. The returned slice is safe to mutate.
func (r *auditRingBuffer) Snapshot() []domain.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]domain.AuditEvent, r.head)
		copy(out, r.buf[:r.head])
		return out
	}
	// Buffer has wrapped: oldest entry is at r.head, newest at r.head-1.
	out := make([]domain.AuditEvent, r.cap)
	copy(out, r.buf[r.head:])
	copy(out[r.cap-r.head:], r.buf[:r.head])
	return out
}

// Len reports the number of entries currently stored (up to cap).
func (r *auditRingBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return r.cap
	}
	return r.head
}
