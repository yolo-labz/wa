package whatsmeow

import (
	"strconv"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// streamSendTimeout is the per-event push deadline enforced by
// enqueueBounded. 100 ms is the R-04 spec value — long enough that a
// slow consumer blink does not trigger drops, short enough that a
// stuck consumer is surfaced before the upstream whatsmeow loop backs
// up further.
const streamSendTimeout = 100 * time.Millisecond

// eventRingBuffer is the bounded history used by ResumeFrom to replay
// recent events. It is intentionally a simple slice with len-bounded
// append-evict semantics: the whatsmeow dispatch loop pushes ~low
// hundreds of events per second at peak, and the capacity matches
// eventCh so a full buffer + full ring == worst case.
type eventRingBuffer struct {
	mu  sync.Mutex
	buf []seqEvent
	cap int
}

type seqEvent struct {
	seq uint64
	evt domain.Event
}

func newEventRingBuffer(capacity int) *eventRingBuffer {
	return &eventRingBuffer{cap: capacity, buf: make([]seqEvent, 0, capacity)}
}

// push records (seq, evt) in the ring, evicting the oldest entries
// once cap is exceeded. Drops are silent at this layer — the caller
// is responsible for emitting AuditStreamDrop / StreamDropEvent when
// the primary eventCh push times out.
func (r *eventRingBuffer) push(seq uint64, evt domain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, seqEvent{seq: seq, evt: evt})
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
}

// since returns every buffered event whose sequence is strictly
// greater than `seq`, along with a gap flag indicating that the
// caller's seq predates the oldest buffered entry (i.e. some events
// between seq+1 and oldest-1 are permanently unavailable).
func (r *eventRingBuffer) since(seq uint64) (events []domain.Event, gap bool, oldest uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil, false, 0
	}
	oldest = r.buf[0].seq
	if seq+1 < oldest {
		gap = true
	}
	for _, e := range r.buf {
		if e.seq > seq {
			events = append(events, e.evt)
		}
	}
	return events, gap, oldest
}

// enqueueBounded pushes evt onto eventCh with a streamSendTimeout
// deadline. On success it returns true; on timeout it records an
// AuditStreamDrop entry, emits a synthetic StreamDropEvent
// best-effort into the same channel, and returns false. The timer is
// stopped in all branches to avoid goroutine leaks.
//
// The implementation deliberately does NOT call a.enqueue to avoid a
// recursion: the StreamDropEvent itself must not loop back through
// the timeout path if the channel is *still* full.
func (a *Adapter) enqueueBounded(seq uint64, evt domain.Event) bool {
	// Fast path: non-blocking send, common case when consumer keeps up.
	select {
	case a.eventCh <- evt:
		a.eventRing.push(seq, evt)
		return true
	case <-a.clientCtx.Done():
		return false
	default:
	}
	// Slow path: wait up to streamSendTimeout for a slot.
	t := time.NewTimer(streamSendTimeout)
	defer t.Stop()
	select {
	case a.eventCh <- evt:
		a.eventRing.push(seq, evt)
		return true
	case <-a.clientCtx.Done():
		return false
	case <-t.C:
		a.recordAuditDetail(
			domain.AuditStreamDrop,
			domain.JID{},
			"buffer_full",
			"seq="+strconv.FormatUint(seq, 10),
		)
		a.emitStreamDrop(seq, seq, "buffer_full")
		return false
	}
}

// emitStreamDrop constructs and best-effort sends a StreamDropEvent
// covering [fromSeq, toSeq]. Dropping the drop itself is acceptable —
// the audit ring buffer still records the underlying event loss.
func (a *Adapter) emitStreamDrop(fromSeq, toSeq uint64, reason string) {
	dropID := a.eventSeq.Add(1)
	drop := domain.StreamDropEvent{
		ID:           domain.EventID(strconv.FormatUint(dropID, 10)),
		TS:           a.nowFn(),
		FromSeq:      fromSeq,
		ToSeq:        toSeq,
		DroppedCount: toSeq - fromSeq + 1,
		Reason:       reason,
	}
	select {
	case a.eventCh <- drop:
		a.eventRing.push(dropID, drop)
	default:
	}
}
