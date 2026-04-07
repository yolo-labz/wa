package whatsmeow

import "errors"

// Sentinel errors surfaced by the whatsmeow adapter.
var (
	// ErrNotFound is returned by Lookup/Get for unknown contacts/groups.
	ErrNotFound = errors.New("whatsmeow: not found")
	// ErrUnknownEvent is returned by EventStream.Ack when the id is
	// unknown (currently only the zero id, because SynchronousAck=true
	// means per-id tracking is unnecessary).
	ErrUnknownEvent = errors.New("whatsmeow: unknown event id")
)
