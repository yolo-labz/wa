package app

import (
	"errors"
	"math"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Wire-code literals for the sentinel translation below. They mirror the
// named constants in internal/adapters/primary/socket/errcodes.go, which
// remains the documentation home for the code bands; TestRPCWireMatchesSocketCodes
// in that package pins the two together so neither can drift.
const (
	wireUpstreamError          int32 = -32000
	wirePolicyRefused          int32 = -32100
	wireIdempotencyCollision   int32 = -32101
	wireMediaTooLarge          int32 = -32201
	wireUnsupportedMessageType int32 = -32300
	wireMediaNotCached         int32 = -32301
	wireMessageUnknown         int32 = -32302
	wireInternalError          int32 = -32603
)

// MsgInternalError is the only body a -32603 ever carries. Exported because
// the REST adapter has its own -32603 sites (panic recovery, media route I/O
// failures) that never reach RPCWire; without a shared constant the same code
// went out as "Internal error" from the dispatcher and "internal error" from
// the routes, so a client could not match on it. One code, one string.
const MsgInternalError = "Internal error"

// RPCWire maps a dispatcher error to the (code, message) pair that crosses
// the JSON-RPC boundary, for every primary transport. The code is int32 to
// match jrpc2.Code's underlying type, so the socket adapter converts without
// narrowing.
//
// It exists because this translation used to live only in the socket
// adapter's toRPCError, and the REST adapter reimplemented a subset of it —
// errors.As over the codedError interface and nothing else. The sentinels
// below (domain.ErrMediaNotCached, ErrNotAllowlisted, domain.ErrBlocked, …)
// are plain errors.New values with no RPCCode method, so over `--remote`
// every one of them collapsed to -32603 "internal error". An allowlist
// refusal, an unknown message id and a genuine daemon fault were
// indistinguishable to a remote caller. One table, both transports.
//
// Message policy is part of the contract, not incidental formatting:
//   - ErrNotAllowlisted returns a CONSTANT message (FR-050) so the wire body
//     is byte-identical for any refused (jid, method) pair — no JID probing.
//   - Every other typed error carries err.Error(), which names the offending
//     id/field. Untyped errors never do: they fall to the constant
//     "Internal error" so sqlite paths and upstream detail stay server-side.
func RPCWire(err error) (int32, string) {
	if err == nil {
		return 0, ""
	}

	switch {
	case errors.Is(err, domain.ErrMessageTooLarge):
		return wireMediaTooLarge, detail("MediaTooLarge", err)
	case errors.Is(err, domain.ErrIdempotencyCollision):
		return wireIdempotencyCollision, detail("IdempotencyCollision", err)
	case errors.Is(err, domain.ErrOutsideEditWindow),
		errors.Is(err, domain.ErrPastMuteTimestamp),
		errors.Is(err, domain.ErrBlocked),
		errors.Is(err, domain.ErrNotAdmin),
		errors.Is(err, domain.ErrEmptyGroupPatch),
		errors.Is(err, domain.ErrBroadcastForbidden):
		return wirePolicyRefused, detail("PolicyRefused", err)
	case errors.Is(err, ErrNotAllowlisted):
		// FR-050: constant body, no detail. Must stay ahead of the
		// codedError branch below, which would otherwise surface the
		// sentinel's own -32012.
		return wirePolicyRefused, "PolicyRefused"
	case errors.Is(err, domain.ErrUpstreamError):
		// -32000 is a shared slot with PeerCredRejected / ProtocolMismatch
		// per the JSON-RPC v2 contract; the message disambiguates.
		return wireUpstreamError, "upstream_error: " + err.Error()
	case errors.Is(err, domain.ErrMediaUnsupported):
		return wireUnsupportedMessageType, detail("UnsupportedMessageType", err)
	case errors.Is(err, domain.ErrMediaNotCached):
		return wireMediaNotCached, detail("MediaNotCached", err)
	case errors.Is(err, domain.ErrMessageUnknown):
		return wireMessageUnknown, detail("MessageUnknown", err)
	}

	// Errors carrying their own code (rpcErr, sockettest.RPCError, …).
	// The message is the coded error's OWN text, not the wrapped chain —
	// preserving the wire strings the socket adapter has always emitted.
	var coded interface {
		error
		RPCCode() int
	}
	if errors.As(err, &coded) {
		// RPCCode is an int; JSON-RPC codes are int32. A value outside that
		// range is not a code this daemon defines, so it degrades to the
		// opaque internal error rather than wrapping around.
		c := coded.RPCCode()
		if c < math.MinInt32 || c > math.MaxInt32 {
			return wireInternalError, MsgInternalError
		}
		return int32(c), coded.Error()
	}

	return wireInternalError, MsgInternalError
}

// detail formats a typed error as "<Name>: <full error chain>". The chain is
// what names the offending id — "MediaNotCached: mediaadapter: 3BB9…: …" —
// so a caller can tell WHICH input was rejected without a second round trip.
func detail(name string, err error) string {
	return name + ": " + err.Error()
}
