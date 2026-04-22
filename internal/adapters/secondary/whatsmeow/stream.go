package whatsmeow

import (
	"context"
	"strconv"

	"github.com/yolo-labz/wa/internal/domain"
)

// Next implements app.EventStream. Priority: resume replay queue → porttest
// test queue → live eventCh. Per ports.go §ES2, ctx cancellation returns
// ctx.Err(). Every delivered non-zero id is recorded so Ack can distinguish
// known vs unknown (ES5).
func (a *Adapter) Next(ctx context.Context) (domain.Event, error) {
	a.resumeMu.Lock()
	if len(a.resumeQueue) > 0 {
		evt := a.resumeQueue[0]
		a.resumeQueue = a.resumeQueue[1:]
		a.resumeMu.Unlock()
		a.recordDelivered(evt.EventID())
		return evt, nil
	}
	a.resumeMu.Unlock()

	if evt, ok := a.popTestEvent(); ok {
		a.recordDelivered(evt.EventID())
		return evt, nil
	}

	select {
	case evt := <-a.eventCh:
		a.recordDelivered(evt.EventID())
		return evt, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.clientCtx.Done():
		return nil, a.clientCtx.Err()
	}
}

// Ack implements app.EventStream. The whatsmeow adapter uses
// SynchronousAck=true at the transport layer, which means whatsmeow has
// already acked the upstream message by the time Next returns. The
// domain-level Ack is therefore a no-op for known ids; per ES5 it must
// return a typed error for a zero id or any id that was never delivered
// by Next.
func (a *Adapter) Ack(id domain.EventID) error {
	if id.IsZero() {
		return ErrUnknownEvent
	}
	if !a.isDelivered(id) {
		return ErrUnknownEvent
	}
	return nil
}

// ResumeFrom implements app.EventStream. Subsequent Next() calls will
// replay every ring-buffered event with sequence > seq. If seq predates
// the ring's oldest entry, a synthetic StreamDropEvent is queued FIRST
// so the consumer sees the gap (CLAUDE.md rule 12 — no silent fallbacks).
// Feature 018 T1-13 / R-04.
func (a *Adapter) ResumeFrom(seq uint64) error {
	events, gap, oldest := a.eventRing.since(seq)

	a.resumeMu.Lock()
	defer a.resumeMu.Unlock()

	// Clear any stale queue left over from a prior ResumeFrom. We do
	// NOT try to preserve pending items — the caller just asked to
	// restart from a specific seq, that supersedes whatever was queued.
	a.resumeQueue = a.resumeQueue[:0]

	if gap {
		dropID := a.eventSeq.Add(1)
		drop := domain.StreamDropEvent{
			ID:           domain.EventID(strconv.FormatUint(dropID, 10)),
			TS:           a.nowFn(),
			FromSeq:      seq + 1,
			ToSeq:        oldest - 1,
			DroppedCount: oldest - seq - 1,
			Reason:       "resume_gap",
		}
		a.resumeQueue = append(a.resumeQueue, drop)
		a.recordAuditDetail(
			domain.AuditStreamDrop,
			domain.JID{},
			"resume_gap",
			"from="+strconv.FormatUint(seq+1, 10)+" to="+strconv.FormatUint(oldest-1, 10),
		)
	}
	a.resumeQueue = append(a.resumeQueue, events...)
	return nil
}
