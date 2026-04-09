package app

// AppEvent is the app-layer event type delivered by the EventBridge.
// It is structurally similar to the socket adapter's Event type but owned
// by internal/app/ to avoid importing adapter packages (research D2).
type AppEvent struct {
	// Type is the event type name: "message", "receipt", "status", "pairing".
	Type string
	// Payload is the domain event, marshaled by the composition root adapter.
	Payload any
}
