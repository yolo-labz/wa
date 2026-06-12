package rest

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Dispatcher is the same contract the socket primary adapter uses
// (internal/adapters/primary/socket/dispatcher.go). Spec 110a defines
// it locally so the rest adapter does not import the socket package
// — both adapters depend on the same use case layer, not on each
// other.
type Dispatcher interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

// EventStream is the spec 110b extension surface — emits server-pushed
// events to the SSE handler. Implementers MUST be safe for concurrent
// use; multiple SSE clients each call SubscribeStream independently.
//
// Event is intentionally a thin envelope wrapping pre-marshalled JSON
// so the rest package does not depend on internal/app types.
type EventStream interface {
	SubscribeStream(filter []string, bufSize int) (ch <-chan Event, cancel func())
}

// Event is the wire-ready shape the SSE handler emits. Seq is the
// monotonic event sequence number (used for Last-Event-ID replay
// in a future spec). Type is the event kind (e.g. "message",
// "receipt"). Data is opaque pre-marshalled JSON of the payload.
type Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ReplayRecord is one durable-ring row the SSE handler emits during
// Last-Event-ID resume (spec 110b v1, ARCH-01 wire). Seq == 0 marks a
// synthetic frame (e.g. the FR-063 stream.drop gap signal) — the handler
// omits the `id:` line for those so the client's cursor only ever
// advances along real ring seqs.
type ReplayRecord struct {
	Seq  int64
	Kind string
	Data []byte
}

// EventReplayer is the optional durable-ring read surface behind
// GET /v1/events. When wired, the SSE handler becomes a store-tail
// reader: frames carry the ring's monotonic seq as the SSE id, so a
// reconnecting client resumes gaplessly via Last-Event-ID (or ?since=).
//
// Replay returns records with seq > sinceSeq in ascending order, at
// most limit; the first batch MAY be prefixed with a synthetic
// stream.drop record when the cursor has fallen below the ring's oldest
// retained seq. NewestSeq reports the current ring head so a fresh
// connection (no cursor) tails from "now" instead of replaying history.
type EventReplayer interface {
	Replay(ctx context.Context, sinceSeq int64, limit int) ([]ReplayRecord, error)
	NewestSeq(ctx context.Context) (int64, error)
}

// MediaResolver is the minimal media port the REST adapter needs to
// stream content-addressed bytes (issue #169). It mirrors the Resolve
// half of app.MediaStore and is declared locally so the primary
// adapter depends on the use-case contract, not the concrete adapter.
// A miss MUST surface as an error matching os.ErrNotExist so the
// handler can map it to 404.
type MediaResolver interface {
	Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error)
}

// MediaWriter is the minimal media port the POST /media/upload route
// needs to persist a client-uploaded payload under its content-addressed
// path (spec 198). It mirrors the Write half of app.MediaStore and is
// declared locally so the primary adapter depends on the use-case
// contract, not the concrete adapter. Write MUST be atomic and idempotent
// (a repeated Write for the same sha returns the existing object), and the
// implementation derives the on-disk path solely from ref — never from any
// client-supplied string — so no path traversal is reachable through it.
type MediaWriter interface {
	Write(ctx context.Context, ref domain.MediaRef, payload []byte, advertisedMime string, duration int64) (domain.MediaObject, error)
}
