package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// sseHeartbeatInterval is the cadence at which the SSE handler emits
// a comment frame to defeat intermediary idle timeouts (nginx default
// 60s, cloudflare 100s). 25s is well below both. Codex review
// recommendation per spec 110.
const sseHeartbeatInterval = 25 * time.Second

// sseSubscriberBuffer caps the per-client event buffer. When full,
// EventBridge.Run drops oldest events for that subscriber, preserving
// liveness over completeness. 256 covers typical inbound burst rates
// with substantial headroom.
const sseSubscriberBuffer = 256

// sseReplayBatch caps one EventReplayer.Replay call. 500 keeps a full
// 10k-ring replay at 20 round-trips while bounding per-batch memory.
const sseReplayBatch = 500

// ssePumpSettle is the single retry delay between a live wake-hint and a
// second ring poll. The hint and the durable append race on the same
// fan-out (the pump is a peer subscriber), so the first poll after a
// hint can run before the append commits; one settle retry covers the
// sqlite commit latency without a busy poll loop.
const ssePumpSettle = 50 * time.Millisecond

// handleEvents implements GET /v1/events as Server-Sent Events.
// Spec 110b. Auth is required. Response stays open for the life of
// the request; client disconnect propagates via r.Context().Done().
//
// Frame shape per event:
//
//	id: <seq>\n
//	event: <type>\n
//	data: <json payload>\n
//	\n
//
// Plus a heartbeat comment frame (`: keepalive\n\n`) every
// sseHeartbeatInterval.
//
// With an EventReplayer wired (spec 110b v1, ARCH-01) the handler is a
// store-tail reader: every frame's id is the durable ring's monotonic
// seq, `Last-Event-ID` (standard EventSource reconnect) or `?since=`
// resumes gaplessly from that cursor, and a cursor that has fallen off
// the ring is signalled with a synthetic `stream.drop` frame (FR-063).
// The live EventStream subscription then serves as the wake signal for
// ring polls. Without a replayer the v0 live-only behaviour is kept
// byte-for-byte: per-connection ids, lossy cursor.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.events == nil {
		// EventStream not wired — fail fast with a JSON-RPC error
		// envelope (REST clients parse this uniformly).
		w.Header().Set("Content-Type", "application/json")
		s.writeError(w, nil, http.StatusServiceUnavailable, -32099, "events stream not available")
		return
	}
	if err := s.auth.Verify(r); err != nil {
		w.Header().Set("Content-Type", "application/json")
		s.writeError(w, nil, http.StatusUnauthorized, -32099, "unauthorized")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// HTTP/2 always supports flushing; HTTP/1.1 does too as long
		// as no middleware buffers the response. The fallback
		// indicates a misconfigured proxy.
		s.writeError(w, nil, http.StatusInternalServerError, -32603, "streaming not supported")
		return
	}

	// Resolve the resume cursor BEFORE committing the 200 + SSE headers —
	// a malformed Last-Event-ID must surface as a clean 400, not a JSON
	// error frame injected mid-stream.
	var cursor int64
	if s.replay != nil {
		var cursorOK bool
		cursor, cursorOK = s.resumeCursor(w, r)
		if !cursorOK {
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defeat nginx's default response buffering for SSE. Per
	// research dossier (PR 109): without this, events queue at
	// the proxy until the response closes.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Initial comment so the client can detect connection
	// readiness without waiting for the first event.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch, cancel := s.events.SubscribeStream(nil, sseSubscriberBuffer)
	defer cancel()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	ctx := r.Context()

	if s.replay != nil {
		s.tailEvents(w, flusher, r, ch, heartbeat.C, cursor)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case evt, ok := <-ch:
			if !ok {
				// Channel closed by EventBridge.Close() — the daemon
				// is shutting down. Drop the connection cleanly.
				return
			}
			if err := writeEvent(w, evt); err != nil {
				// Write error → connection broken or proxy timeout.
				// Returning lets defer cancel() deregister our
				// subscriber so EventBridge doesn't accumulate stale
				// waiters.
				return
			}
			flusher.Flush()
		}
	}
}

// sseTail bundles the per-connection state of the spec 110b v1
// store-tail loop so the steps stay small (gocyclo) without threading
// five parameters through every helper.
type sseTail struct {
	s       *Server
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
	cursor  int64
}

// tailEvents is the spec 110b v1 store-tail loop: replay the durable
// ring from the client's cursor, then poll it forward on live wake
// hints (and on the heartbeat tick as a lost-hint safety net). All
// frames carry real ring seqs; the live channel's payloads are never
// emitted directly.
func (s *Server) tailEvents(w http.ResponseWriter, flusher http.Flusher, r *http.Request, ch <-chan Event, heartbeat <-chan time.Time, cursor int64) {
	t := &sseTail{s: s, w: w, flusher: flusher, ctx: r.Context(), cursor: cursor}

	if !t.emit() {
		return
	}

	for {
		select {
		case <-t.ctx.Done():
			return

		case <-heartbeat:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			// Safety net for wake hints lost to a full subscriber
			// buffer: the ring is the source of truth, so a periodic
			// poll guarantees ≤25 s staleness even with no hints.
			if !t.emit() {
				return
			}

		case _, ok := <-ch:
			if !ok {
				return // EventBridge closed — daemon shutting down.
			}
			if !t.onWakeHint(ch) {
				return
			}
		}
	}
}

// emit drains the ring from the cursor and returns false on a write or
// replay error (either way the connection is done).
func (t *sseTail) emit() bool {
	for {
		recs, err := t.s.replay.Replay(t.ctx, t.cursor, sseReplayBatch)
		if err != nil {
			t.s.log.Error("sse replay", "err", err, "cursor", t.cursor)
			return false
		}
		if len(recs) == 0 {
			return true
		}
		for _, rec := range recs {
			if err := writeReplayEvent(t.w, rec); err != nil {
				return false
			}
			if rec.Seq > t.cursor {
				t.cursor = rec.Seq
			}
		}
		t.flusher.Flush()
		if len(recs) < sseReplayBatch {
			return true
		}
	}
}

// onWakeHint coalesces the hint backlog, polls the ring, and re-polls
// once after ssePumpSettle when the hint outran the pump's append.
// Returns false when the connection is done.
func (t *sseTail) onWakeHint(ch <-chan Event) bool {
	for drained := false; !drained; {
		select {
		case _, more := <-ch:
			if !more {
				return false
			}
		default:
			drained = true
		}
	}
	before := t.cursor
	if !t.emit() {
		return false
	}
	if t.cursor != before {
		return true
	}
	// Hint outran the pump's append — settle once, re-poll.
	select {
	case <-time.After(ssePumpSettle):
	case <-t.ctx.Done():
		return false
	}
	return t.emit()
}

// resumeCursor derives the ring cursor for this connection:
// Last-Event-ID header (standard EventSource reconnect) first, then the
// ?since= query param, else the ring head (a fresh connection tails from
// "now" — history replay is opt-in via an explicit cursor, matching the
// v0 attach-time semantics). A non-numeric or negative cursor is a 400:
// failing loud beats silently replaying the whole ring. Returns ok=false
// when a response has already been written.
func (s *Server) resumeCursor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("since")
	}
	if raw == "" {
		newest, err := s.replay.NewestSeq(r.Context())
		if err != nil {
			s.log.Error("sse newest seq", "err", err)
			s.writeError(w, nil, http.StatusServiceUnavailable, -32099, "events ring unavailable")
			return 0, false
		}
		return newest, true
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		s.writeError(w, nil, http.StatusBadRequest, -32602, "Last-Event-ID / since must be a non-negative integer seq")
		return 0, false
	}
	return cursor, true
}

// writeReplayEvent emits one durable-ring SSE frame. Synthetic records
// (Seq == 0, e.g. stream.drop) carry no id: line so the client cursor
// only advances along real ring seqs.
func writeReplayEvent(w http.ResponseWriter, rec ReplayRecord) error {
	if rec.Seq > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", rec.Seq); err != nil {
			return err
		}
	}
	if rec.Kind != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", rec.Kind); err != nil {
			return err
		}
	}
	data := rec.Data
	if len(data) == 0 {
		data = []byte("null")
	}
	_, err := fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// writeEvent emits one SSE frame. Returns the underlying write error
// so the caller can break out of the loop on connection breakage.
func writeEvent(w http.ResponseWriter, evt Event) error {
	if _, err := fmt.Fprintf(w, "id: %d\n", evt.Seq); err != nil {
		return err
	}
	if evt.Type != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", evt.Type); err != nil {
			return err
		}
	}
	// Data may legitimately be nil; emit an empty data: line so the
	// client still parses the frame as an event delivery.
	data := evt.Data
	if data == nil {
		data = json.RawMessage("null")
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}
