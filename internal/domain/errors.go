// Package domain contains the pure-Go entities and invariants of the wa
// project. Files under this package MUST NOT import "go.mau.fi/whatsmeow"
// or any non-stdlib package: this is enforced mechanically by the
// "core-no-whatsmeow" depguard rule in .golangci.yml.
package domain

import "errors"

// Sentinel errors. Every error returned from internal/domain MUST wrap one
// of these via fmt.Errorf("%w: ...", ErrXxx) so callers can errors.Is for
// the category.
var (
	// ErrInvalidJID indicates a malformed or zero JID.
	ErrInvalidJID = errors.New("domain: invalid JID")
	// ErrInvalidPhone indicates a phone number outside ITU-T E.164 [8,15].
	ErrInvalidPhone = errors.New("domain: invalid phone number")
	// ErrEmptyBody indicates a required body or path was empty.
	ErrEmptyBody = errors.New("domain: message body must not be empty")
	// ErrMessageTooLarge indicates a message exceeds its variant size limit.
	ErrMessageTooLarge = errors.New("domain: message exceeds size limit")
	// ErrUnknownAction indicates ParseAction received an unknown string.
	ErrUnknownAction = errors.New("domain: unknown action")
	// ErrNotAllowed is reserved for the app-layer policy middleware.
	ErrNotAllowed = errors.New("domain: action not allowed for jid")
	// ErrInvalidSession indicates a session parameter is invalid (e.g. zero deviceID).
	ErrInvalidSession = errors.New("domain: invalid session")
	// ErrDisconnected is returned by MessageSender.Send when the underlying
	// adapter is in a disconnected state. The caller decides whether to retry,
	// queue, or surface the failure; the adapter never queues silently.
	//
	// Added by feature 003 (whatsmeow secondary adapter) to support FR-018.
	ErrDisconnected = errors.New("domain: adapter disconnected")

	// ErrIdempotencyCollision indicates the supplied idempotency key
	// already exists in the sidecar with a different params_hash (same
	// method + profile + key, different canonical-JSON params). Per
	// FR-034a this maps to JSON-RPC code -32101 at the socket boundary.
	// Distinct from ErrIdempotencyConflict (feature 017 replay fingerprint
	// check) which is being superseded.
	ErrIdempotencyCollision = errors.New("domain: idempotency key collision")

	// ErrPastMuteTimestamp is returned by ChatStateManager.Mute when the
	// supplied until timestamp is not strictly in the future. Maps to
	// -32100 policy_refused at the socket boundary (FR-016).
	ErrPastMuteTimestamp = errors.New("domain: mute timestamp must be in the future")

	// ErrBlocked indicates the target JID appears on the server-side
	// blocklist at send time. Returned by the dispatcher's pre-send gate
	// when Blocker is wired (FR-018/FR-019) so send / sendMedia / react /
	// send.reply fail fast with -32100 policy_refused at the socket
	// boundary. Wrap via fmt.Errorf so callers can errors.Is for the
	// category.
	ErrBlocked = errors.New("domain: recipient is blocked")

	// ErrUpstreamError indicates a server- or library-side failure the
	// adapter cannot correct — e.g. whatsmeow's 3-pin cap rejection,
	// expired group-invite link, unsupported helper on the pinned commit.
	// Maps to -32000 upstream_error at the socket boundary (the -32000 slot
	// is shared with CodePeerCredRejected/CodeProtocolMismatch per the
	// contract; the message field disambiguates). Wrap via fmt.Errorf
	// so callers can errors.Is for the category.
	ErrUpstreamError = errors.New("domain: upstream error")
)
