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

	// ErrNotAdmin indicates the caller is not a group admin and therefore
	// cannot perform the requested roster/metadata mutation
	// (AddParticipants, RemoveParticipants, Promote, Demote, Edit).
	// Mapped to -32100 policy_refused at the socket boundary. Wrap via
	// fmt.Errorf so callers can errors.Is for the category. Feature 018
	// T2-16 / T2-17, FR-021..FR-025.
	ErrNotAdmin = errors.New("domain: not a group admin")

	// ErrEmptyGroupPatch indicates a GroupAdmin.Edit call was made with
	// a GroupPatch whose every field is nil — there is nothing to change.
	// GroupPatch.Validate returns this sentinel so the dispatcher maps
	// the refusal to -32100 policy_refused per the FR-024 contract.
	// Feature 018 T2-17.
	ErrEmptyGroupPatch = errors.New("domain: group patch has no fields set")

	// ErrUpstreamError indicates a server- or library-side failure the
	// adapter cannot correct — e.g. whatsmeow's 3-pin cap rejection,
	// expired group-invite link, unsupported helper on the pinned commit.
	// Maps to -32000 upstream_error at the socket boundary (the -32000 slot
	// is shared with CodePeerCredRejected/CodeProtocolMismatch per the
	// contract; the message field disambiguates). Wrap via fmt.Errorf
	// so callers can errors.Is for the category.
	ErrUpstreamError = errors.New("domain: upstream error")

	// ErrMediaUnsupported indicates a media.download target whose message
	// proto exists but has no downloadable payload — text, reaction,
	// revoke, poll, quoted-only, missing media sub-message, view-once
	// already consumed, etc. Distinct from ErrMediaNotCached, which means
	// the raw protobuf is absent from local history storage. Callers can
	// branch accordingly: ErrMediaUnsupported is permanent (will never
	// download), while ErrMediaNotCached is recoverable via re-sync. Maps
	// to -32300 UnsupportedMessageType at the socket boundary. Issue #102.
	ErrMediaUnsupported = errors.New("domain: message has no downloadable media")

	// ErrMediaNotCached indicates a media.download target whose raw
	// protobuf is absent from the local history store — typically a row
	// synced before raw_proto was persisted (pre-v3 schema), or a row
	// missing from the messages table entirely. Recoverable: re-syncing
	// the chat or running `wa migrate` may restore the proto. Distinct
	// from ErrMediaUnsupported, where the proto exists but does not
	// contain downloadable media. Maps to -32301 MediaNotCached at the
	// socket boundary. Issue #102.
	ErrMediaNotCached = errors.New("domain: message proto not cached")

	// ErrBroadcastForbidden indicates the parser refused a
	// `<digits>@broadcast` JID because broadcast lists are a
	// hard-banned pattern per CLAUDE.md §Safety: "no broadcast lists
	// ever". WhatsApp's anti-spam heuristics flag broadcast traffic
	// aggressively, and the daemon refuses to participate to keep the
	// session healthy. Maps to -32100 PolicyRefused at the socket
	// boundary. Spec 108.
	ErrBroadcastForbidden = errors.New("domain: broadcast lists are forbidden by safety policy")

	// ErrSessionWiped indicates the running process destroyed its own
	// device store — `wa panic`, or the `session.logout` that `wa pair
	// --reset` issues first — and therefore cannot complete a pairing
	// handshake until it is restarted. The upstream device is retired
	// in place (whatsmeow store.Device.Delete nils the ID, sets Deleted,
	// and swaps every sub-store for a NoopStore returning
	// ErrDeviceDeleted), and the adapter acquires its device exactly once
	// at Open — so there is no in-process path back to a pairable client.
	//
	// The refusal is the contract: without it the pair call answers
	// `paired: true` having contacted nothing, and `health` on the same
	// daemon answers `paired: false` from the store. Maps to -32019
	// session_wiped at the transport boundary. Issue #310.
	ErrSessionWiped = errors.New("domain: session store was wiped in this process")
)
