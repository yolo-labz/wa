package socket

// ErrorCode is a JSON-RPC 2.0 error code. Codes in the range -32768..-32000
// are reserved by the specification; -32000..-32099 is the implementation-defined
// "Server error" sub-range. This feature (004) owns -32000..-32010. Feature 005
// owns -32011..-32020. See contracts/wire-protocol.md for the full table.
type ErrorCode int

// Standard JSON-RPC 2.0 protocol error codes (spec §5.1).
const (
	CodeParseError     ErrorCode = -32700
	CodeInvalidRequest ErrorCode = -32600
	CodeMethodNotFound ErrorCode = -32601
	CodeInvalidParams  ErrorCode = -32602
	CodeInternalError  ErrorCode = -32603
)

// Server-defined error codes owned by feature 004 (socket adapter).
const (
	// CodePeerCredRejected indicates the peer uid at accept time did not match
	// the server's effective uid.
	CodePeerCredRejected ErrorCode = -32000

	// CodeBackpressure indicates the per-connection outbound mailbox is full
	// and the subscription-bearing connection is about to be closed.
	CodeBackpressure ErrorCode = -32001

	// CodeShutdownInProgress indicates a new request arrived after the server
	// began graceful shutdown.
	CodeShutdownInProgress ErrorCode = -32002

	// CodeRequestTimeoutDuringShutdown indicates an in-flight request was
	// cancelled because the drain deadline elapsed during graceful shutdown.
	CodeRequestTimeoutDuringShutdown ErrorCode = -32003

	// CodeOversizedMessage indicates a single framed message exceeded the
	// 1 MiB cap.
	CodeOversizedMessage ErrorCode = -32004

	// CodeSubscriptionClosed indicates the dispatcher's event source closed
	// while the connection held an active subscription.
	CodeSubscriptionClosed ErrorCode = -32005

	// CodePongTimeout indicates the client failed to respond to a heartbeat
	// ping within the pong deadline (FR-062). The adapter emits
	// subscribe.closed{reason:"pong_timeout", resumeSince} and closes the
	// subscription.
	CodePongTimeout ErrorCode = -32006

	// CodeStreamDrop indicates the ring buffer evicted events while the
	// subscription was live; the client MUST treat its cursor as lossy and
	// reconcile from the history store (FR-063).
	CodeStreamDrop ErrorCode = -32007

	// CodeProtocolMismatch indicates the first frame was not a well-formed
	// system.hello{protoVersion:2} within the 5s accept budget. Feature 018
	// FR-012 handshake gate. Shares the -32000 slot with PeerCredRejected
	// per the JSON-RPC v2 contract "-32000: protocol_mismatch or upstream
	// error"; the error "message" field disambiguates.
	CodeProtocolMismatch ErrorCode = -32008
)

// Feature 018 error codes (-32100..-32199). Policy / idempotency.
const (
	// CodePolicyRefused indicates an allowlist / edit-window / scope refusal.
	CodePolicyRefused ErrorCode = -32100
	// CodeIdempotencyCollision indicates a same-key replay with a different
	// params_hash (FR-034a).
	CodeIdempotencyCollision ErrorCode = -32101
)

// Feature 018 rate-limit / upstream codes (-32200..-32299).
const (
	// CodeRateLimited indicates the per-second/minute/day ladder refused.
	CodeRateLimited ErrorCode = -32200
	// CodeMediaTooLarge indicates the payload exceeded the 16 MiB ceiling.
	CodeMediaTooLarge ErrorCode = -32201
)

// Issue #102 media-lookup codes (-32300..-32399). Disambiguate failures
// that previously surfaced as opaque -32603 Internal error from
// media.download for legacy / non-media messages.
const (
	// CodeUnsupportedMessageType indicates the target message has no
	// downloadable payload (text, reaction, revoke, view-once consumed,
	// poll, quoted-only). Permanent — caller MUST NOT retry.
	CodeUnsupportedMessageType ErrorCode = -32300
	// CodeMediaNotCached indicates the target message's raw protobuf is
	// absent from the history store (pre-v3 schema or missing row).
	// Recoverable — caller may prompt for `wa migrate` or chat re-sync.
	CodeMediaNotCached ErrorCode = -32301
	// CodeMessageUnknown indicates the target message row is absent from
	// the history store, so an operation that addresses a message by more
	// than its id — message.revoke scope=self, whose deleteMessageForMe
	// index needs (chat, id, fromMe, participant) — cannot compute its
	// key. Recoverable via chat re-sync. Feature 115.
	CodeMessageUnknown ErrorCode = -32302
)

// Compile-time assertion: no server code falls in the -32011..-32099 reserved
// range that belongs to feature 005 and later features.
func assertServerCodesNotInReservedRange() { //nolint:unused // compile-time assertion, never called
	// The assertion is expressed as array index bounds: if any code falls in
	// [-32099, -32011] the expression (code + 32099) is negative for codes
	// below -32099 and (code + 32011) is positive for codes above -32011.
	// We need to verify each server code is NOT in that range.
	// A code c is in the reserved range iff -32099 <= c <= -32011.
	// Equivalently, c is safe iff c < -32099 OR c > -32011.
	// All our server codes are -32000..-32005 which are > -32011, so safe.
	var _ [1]struct{}

	// Each line asserts the code is outside [-32099, -32011].
	// If the code were in range, the subtraction would produce a value ≤ 0,
	// and the array size would be non-positive → compile error.
	_ = [CodePeerCredRejected - (-32011) + 1]struct{}{}
	_ = [CodeBackpressure - (-32011) + 1]struct{}{}
	_ = [CodeShutdownInProgress - (-32011) + 1]struct{}{}
	_ = [CodeRequestTimeoutDuringShutdown - (-32011) + 1]struct{}{}
	_ = [CodeOversizedMessage - (-32011) + 1]struct{}{}
	_ = [CodeSubscriptionClosed - (-32011) + 1]struct{}{}
	_ = [CodePongTimeout - (-32011) + 1]struct{}{}
	_ = [CodeStreamDrop - (-32011) + 1]struct{}{}
	_ = [CodeProtocolMismatch - (-32011) + 1]struct{}{}
}

// errCodeName maps each error code to a human-readable name suitable for log
// messages and the JSON-RPC error "message" field.
var errCodeName = map[ErrorCode]string{
	// Protocol codes
	CodeParseError:     "Parse error",
	CodeInvalidRequest: "Invalid Request",
	CodeMethodNotFound: "Method not found",
	CodeInvalidParams:  "Invalid params",
	CodeInternalError:  "Internal error",

	// Server codes (feature 004)
	CodePeerCredRejected:             "PeerCredRejected",
	CodeBackpressure:                 "Backpressure",
	CodeShutdownInProgress:           "ShutdownInProgress",
	CodeRequestTimeoutDuringShutdown: "RequestTimeoutDuringShutdown",
	CodeOversizedMessage:             "OversizedMessage",
	CodeSubscriptionClosed:           "SubscriptionClosed",
	CodePongTimeout:                  "PongTimeout",
	CodeStreamDrop:                   "StreamDrop",
	CodeProtocolMismatch:             "ProtocolMismatch",

	// Feature 018.
	CodePolicyRefused:        "PolicyRefused",
	CodeIdempotencyCollision: "IdempotencyCollision",
	CodeRateLimited:          "RateLimited",
	CodeMediaTooLarge:        "MediaTooLarge",

	// Issue #102.
	CodeUnsupportedMessageType: "UnsupportedMessageType",
	CodeMediaNotCached:         "MediaNotCached",
	CodeMessageUnknown:         "MessageUnknown",
}
