package rest

import (
	"context"
	"encoding/json"
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
