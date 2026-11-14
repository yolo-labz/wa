package domain

import "slices"

// ErrorReason is the closed machine enum every wire error carries as
// error.data.reason (spec 116 FR-116-10). JSON-RPC codes collide (-32100
// covers not-allowlisted and other policy refusals; REST's -32099 covers
// both scope and media refusals), so a consumer that needs to know WHAT was
// refused reads the reason, never the code band or the message text.
type ErrorReason string

// The FR-116-10 catalog. It is closed: a new member is a spec change.
const (
	ReasonMessageNotFound   ErrorReason = "message_not_found"
	ReasonMediaNotFound     ErrorReason = "media_not_found"
	ReasonMediaNotCached    ErrorReason = "media_not_cached"
	ReasonMediaUnsupported  ErrorReason = "media_unsupported"
	ReasonMediaTooLarge     ErrorReason = "media_too_large"
	ReasonNotAllowlisted    ErrorReason = "not_allowlisted"
	ReasonPolicyRefused     ErrorReason = "policy_refused"
	ReasonScopeInsufficient ErrorReason = "scope_insufficient"
	ReasonNotPaired         ErrorReason = "not_paired"
	ReasonSessionWiped      ErrorReason = "session_wiped"
	ReasonDisconnected      ErrorReason = "disconnected"
	ReasonRateLimited       ErrorReason = "rate_limited"
	ReasonWarmupActive      ErrorReason = "warmup_active"
	ReasonInvalidParams     ErrorReason = "invalid_params"
	ReasonStorageUnwritable ErrorReason = "storage_unwritable"
	ReasonJobNotFound       ErrorReason = "job_not_found"
	ReasonTimedOut          ErrorReason = "timed_out"
	ReasonCapacityExceeded  ErrorReason = "capacity_exceeded"
	ReasonUpstreamError     ErrorReason = "upstream_error"
	ReasonSendUncertain     ErrorReason = "send_uncertain"
	ReasonInternal          ErrorReason = "internal"
)

var errorReasons = []ErrorReason{
	ReasonMessageNotFound, ReasonMediaNotFound, ReasonMediaNotCached, ReasonMediaUnsupported,
	ReasonMediaTooLarge, ReasonNotAllowlisted, ReasonPolicyRefused, ReasonScopeInsufficient,
	ReasonNotPaired, ReasonSessionWiped, ReasonDisconnected, ReasonRateLimited, ReasonWarmupActive,
	ReasonInvalidParams, ReasonStorageUnwritable, ReasonJobNotFound, ReasonTimedOut,
	ReasonCapacityExceeded, ReasonUpstreamError, ReasonSendUncertain, ReasonInternal,
}

// ErrorReasons returns every member of the closed enum.
func ErrorReasons() []ErrorReason { return slices.Clone(errorReasons) }

// ParseErrorReason returns s as an ErrorReason when it is an exact member of
// the enum and ReasonInternal otherwise. Missing and unknown reasons are
// internal: there is no fallback to the code or the message.
func ParseErrorReason(s string) ErrorReason {
	if slices.Contains(errorReasons, ErrorReason(s)) {
		return ErrorReason(s)
	}
	return ReasonInternal
}

// ErrorData is the JSON-RPC error.data member (and the RFC 9457 extension
// members of a REST problem body). The scope fields are set only when
// Reason is ReasonScopeInsufficient.
type ErrorData struct {
	Reason        ErrorReason `json:"reason"`
	RequiredScope string      `json:"required_scope,omitempty"`
	GrantedScope  string      `json:"granted_scope,omitempty"`
	// MessageID is set with ReasonSendUncertain: the id the send may have used.
	MessageID string `json:"message_id,omitempty"`
}
