package app

// Event is the app-layer event type delivered by the EventBridge.
// It is structurally similar to the socket adapter's Event type but owned
// by internal/app/ to avoid importing adapter packages (research D2).
type Event struct {
	// Type is the event type name: "message", "receipt", "status", "pairing".
	Type string
	// Payload is the domain event, marshaled by the composition root adapter.
	Payload any

	// Seq is the FR-061 per-profile monotonic sequence number the
	// EventBridge stamps on every event it fans out, upstream and
	// synthetic alike. It is what makes the socket adapter's two
	// seq-aware features mean anything: left at 0, FR-063 gap detection
	// never emits a stream.drop frame (server_fanout.go) and a
	// `subscribe({since: N})` resume cursor re-delivers events the client
	// already acked (match.go).
	//
	// Numbering continues above the durable ring's newest row across a
	// restart, so the number a subscriber reads off the socket is the
	// same one events.db files that event under — one namespace, not two.
	Seq int64

	// Chat, Sender and Body are the FR-060 filter-DSL selectors. They are
	// derived from the domain event at translation time and are the only
	// values the socket adapter's `--chats` / `--senders` / `--not-senders`
	// / `--body-re` filters can match on: left empty, a subscription
	// carrying any of those filters matches nothing at all.
	//
	// They never go on the wire. Body in particular is raw sender-authored
	// text, and FR-005a admits it to a subscriber only inside the
	// <channel> envelope of Payload — so the `json:"-"` tags hold even
	// though nothing marshals this struct wholesale today. Matching
	// against the raw body in-process is the point: a regex written
	// against envelope markup would be unusable.
	Chat   string `json:"-"`
	Sender string `json:"-"`
	Body   string `json:"-"`
}
