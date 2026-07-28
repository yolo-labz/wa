// replay.go implements the FR-061 resume half of `subscribe`: handing back
// the buffered events a reconnecting client missed, rather than only
// suppressing the ones it already saw.
package socket

import (
	"context"
	"encoding/json"
)

// replayBatch caps one EventReplayer.Replay call. 500 keeps a full 10 000-slot
// ring drain at 20 round-trips while bounding per-batch memory, matching the
// SSE reader's batch size so both transports load the ring identically.
const replayBatch = 500

// ReplayRecord is one durable-ring row projected for socket delivery.
//
// Data is the subscriber-safe payload the pump appended — the same projection
// the live path marshals — so a replayed frame is byte-identical to the frame
// the client would have received had it stayed connected.
//
// Chat/Sender/Body are the FR-060 filter selectors. They are matched against
// server-side and never emitted, exactly as on the live path, where they are
// `json:"-"` on Event. A replayer that cannot recover them leaves them empty,
// which makes a subscription filtering on them match nothing rather than
// match everything — the safe direction for a filter.
type ReplayRecord struct {
	Seq    int64
	Kind   string
	Data   []byte
	Chat   string
	Sender string
	Body   string
}

// EventReplayer is the optional durable-ring read surface behind
// `subscribe({since: N})`. It mirrors rest.EventReplayer so both transports
// read the same ring through the same shape.
//
// Replay returns records with seq > sinceSeq in ascending order, at most
// limit. OldestSeq reports the ring's oldest retained seq so the caller can
// tell "you asked for events that have been evicted" apart from "you are
// already caught up"; a ring that has never evicted reports its first seq.
type EventReplayer interface {
	Replay(ctx context.Context, sinceSeq int64, limit int) ([]ReplayRecord, error)
	OldestSeq(ctx context.Context) (int64, error)
}

// replaySubscription drains the ring into conn for a subscription that asked
// to resume from a cursor, before that subscription is registered for live
// fan-out.
//
// Ordering is what makes the pre-registration call site load-bearing.
// pushNotification appends to the connection's outbound mailbox, which a
// single writer goroutine drains in FIFO order, so every frame queued here
// reaches the socket ahead of every frame the fan-out queues later. Register
// first and a live event could interleave into the middle of the replay;
// queue-then-register cannot.
//
// The residual race is a live event that fans out in the window between the
// last ring poll and registration: not replayed (the poll missed it) and not
// delivered live (the subscription was not yet visible). That event is
// reported, not lost silently — lastSeq stops at the last replayed seq, so
// the next live event trips the fan-out's gap detector and the client gets an
// FR-063 stream.drop naming it. Closing the window entirely would mean
// buffering live frames inside the subscription across registration; that
// trades a microsecond-wide reported gap for real concurrency risk on the
// daemon's delivery path, which rule 27 says is the wrong trade.
//
// Errors are non-fatal by the same reasoning: a ring read that fails leaves
// the client attached and live rather than failing the subscribe outright,
// and the gap it could not fill is the gap detector's job to announce.
func (s *Server) replaySubscription(ctx context.Context, conn *Connection, sub *Subscription) {
	s.emitEvictionGap(ctx, conn, sub)

	cursor := sub.since
	for {
		recs, err := s.replay.Replay(ctx, cursor, replayBatch)
		if err != nil {
			conn.log.Warn("subscribe replay", "subscription_id", sub.id, "cursor", cursor, "err", err)
			return
		}
		if len(recs) == 0 {
			return
		}
		for _, rec := range recs {
			if rec.Seq > cursor {
				cursor = rec.Seq
			}
			if !s.pushReplayRecord(conn, sub, rec) {
				return
			}
		}
		if len(recs) < replayBatch {
			return
		}
	}
}

// emitEvictionGap tells the client up front when its cursor has fallen off
// the back of the ring, so the events this replay cannot produce are named
// before the ones it can start arriving.
//
// It reuses the fan-out's stream.drop frame rather than inventing a second
// shape: a gap is a gap whether the daemon noticed it at resume time or mid
// stream, and a client that special-cases one encoding would silently miss
// the other. An OldestSeq failure skips only the signal — Replay still
// decides the real outcome.
func (s *Server) emitEvictionGap(ctx context.Context, conn *Connection, sub *Subscription) {
	oldest, err := s.replay.OldestSeq(ctx)
	if err != nil {
		conn.log.Warn("subscribe replay oldest seq", "subscription_id", sub.id, "err", err)
		return
	}
	// oldest == 0 is an empty ring: nothing was retained, so nothing was
	// evicted either. Only a cursor strictly below the oldest retained seq
	// means rows the client wanted are gone.
	if oldest <= sub.since+1 {
		return
	}
	_ = conn.pushNotification(streamDropFrame(sub.id, sub.since+1, oldest-1))
}

// pushReplayRecord delivers one ring row if it passes the subscription's
// filter, advancing lastSeq only on a frame that actually reached the
// mailbox. Returns false when the connection is done and the drain should
// stop.
//
// lastSeq advances on skipped records too. It is the resume cursor, not a
// delivery count: a filtered-out event is one the client asked never to see,
// so leaving the cursor behind it would make the next live event look like a
// gap and emit a stream.drop for events that were deliberately excluded.
func (s *Server) pushReplayRecord(conn *Connection, sub *Subscription, rec ReplayRecord) bool {
	evt := Event{
		Type:    rec.Kind,
		Seq:     rec.Seq,
		Payload: json.RawMessage(rec.Data),
		Chat:    rec.Chat,
		Sender:  rec.Sender,
		Body:    rec.Body,
	}
	if !matchesSub(sub, evt, sub.bodyReCompiled) {
		s.advanceLastSeq(conn, sub, rec.Seq)
		return true
	}
	frame, err := marshalNotification(evt, sub.id)
	if err != nil {
		conn.log.Error("failed to marshal replayed notification", "error", err, "seq", rec.Seq)
		s.advanceLastSeq(conn, sub, rec.Seq)
		return true
	}
	if err := conn.pushNotification(frame); err != nil {
		return false
	}
	s.advanceLastSeq(conn, sub, rec.Seq)
	return true
}

// advanceLastSeq moves the subscription cursor forward under the connection
// lock. The subscription is not registered for fan-out yet, but the heartbeat
// reaper reads lastSeq off any connection, so the write is still shared.
func (s *Server) advanceLastSeq(conn *Connection, sub *Subscription, seq int64) {
	if seq <= 0 {
		return
	}
	conn.mu.Lock()
	if seq > sub.lastSeq {
		sub.lastSeq = seq
	}
	conn.mu.Unlock()
}
