package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
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
// Last-Event-ID replay is NOT implemented in 110b v0 — the existing
// EventBridge has no ring buffer for back-fill. Reconnecting clients
// MUST treat their cursor as lossy and reconcile from history.
// Future work: spec 110b v1 wires a ring + Last-Event-ID replay.
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
