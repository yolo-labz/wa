package domain

import (
	"errors"
	"fmt"
	"strings"
)

// DraftExpiryWindow is the FR-043 sweep horizon: drafts auto-transition to
// expired 30 minutes after creation if no approve/reject decision lands.
const DraftExpiryWindow = 30 * 60 // seconds

// DraftState enumerates the lifecycle states of a Draft. Terminal states
// are approved, rejected, expired; pending_review is the only live state.
type DraftState uint8

// DraftState values. Zero is invalid.
const (
	DraftPendingReview DraftState = iota + 1
	DraftApproved
	DraftRejected
	DraftExpired
)

// String returns the canonical lowercase name used on the wire.
func (s DraftState) String() string {
	switch s {
	case DraftPendingReview:
		return "pending_review"
	case DraftApproved:
		return "approved"
	case DraftRejected:
		return "rejected"
	case DraftExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the four declared states.
func (s DraftState) IsValid() bool {
	return s == DraftPendingReview || s == DraftApproved || s == DraftRejected || s == DraftExpired
}

// IsTerminal reports whether s is a terminal state (no further transitions
// permitted). Only pending_review is non-terminal.
func (s DraftState) IsTerminal() bool {
	return s == DraftApproved || s == DraftRejected || s == DraftExpired
}

// DraftKind enumerates the draftable operation kinds per FR-040.
type DraftKind uint8

// DraftKind values. Zero is invalid.
const (
	DraftKindSend DraftKind = iota + 1
	DraftKindSendMedia
	DraftKindReact
	DraftKindMarkRead
	DraftKindScheduleSend
	DraftKindGroupAddParticipant
	DraftKindGroupCreate
)

// String returns the canonical lowercase name used on the wire.
func (k DraftKind) String() string {
	switch k {
	case DraftKindSend:
		return "send"
	case DraftKindSendMedia:
		return "send_media"
	case DraftKindReact:
		return "react"
	case DraftKindMarkRead:
		return "mark_read"
	case DraftKindScheduleSend:
		return "schedule_send"
	case DraftKindGroupAddParticipant:
		return "group_add_participant"
	case DraftKindGroupCreate:
		return "group_create"
	default:
		return "unknown"
	}
}

// IsValid reports whether k is one of the seven declared kinds.
func (k DraftKind) IsValid() bool {
	return k >= DraftKindSend && k <= DraftKindGroupCreate
}

// AlwaysDraft reports whether this kind MUST be drafted regardless of any
// contact-level `approval: auto` allowlist override per FR-041. Group
// create and group add-participant are always-draft by policy; the
// "first-send to a JID never messaged before in this profile" case is
// resolved at the use-case layer, not here.
func (k DraftKind) AlwaysDraft() bool {
	return k == DraftKindGroupCreate || k == DraftKindGroupAddParticipant
}

// DraftDecider is the identity of whoever transitioned a draft. The sweeper
// uses "timeout"; interactive flows use "cli" or "skill".
type DraftDecider string

// Canonical DraftDecider values.
const (
	DraftDeciderCLI     DraftDecider = "cli"
	DraftDeciderSkill   DraftDecider = "skill"
	DraftDeciderTimeout DraftDecider = "timeout"
)

// IsValid reports whether d is one of the three declared deciders.
func (d DraftDecider) IsValid() bool {
	return d == DraftDeciderCLI || d == DraftDeciderSkill || d == DraftDeciderTimeout
}

// ErrDraftState is the categorical error for all draft transition failures.
// The socket layer maps this to JSON-RPC -32109 draft_state (FR-044).
var ErrDraftState = errors.New("domain: invalid draft state transition")

// ErrDraftInvariant indicates a Draft failed a structural invariant
// (empty id, malformed timestamps, unknown kind, missing decider).
var ErrDraftInvariant = errors.New("domain: draft invariant violation")

// Draft is the aggregate covering every operation the daemon surfaces to a
// human reviewer before the adapter side-effect fires. Exactly one
// PayloadJSON carries the RPC params minus the idempotencyKey per FR-040.
type Draft struct {
	ID          string
	Profile     string
	Kind        DraftKind
	PayloadJSON string
	TargetJID   JID // zero for group.create per data-model.md
	State       DraftState
	CreatedAt   int64
	ExpiresAt   int64
	DecidedAt   int64
	DecidedBy   DraftDecider
	Reason      string
}

// Validate enforces the structural invariants of a newly-constructed or
// freshly-loaded Draft row. It does NOT enforce transition legality; use
// Approve, Reject, ExpireAt for those.
//
//nolint:gocyclo // invariant count tracks schema field count, not
// implementation complexity. Each branch is a discrete schema rule.
func (d Draft) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("%w: id is empty", ErrDraftInvariant)
	}
	if strings.TrimSpace(d.Profile) == "" {
		return fmt.Errorf("%w: profile is empty", ErrDraftInvariant)
	}
	if !d.Kind.IsValid() {
		return fmt.Errorf("%w: kind %d", ErrDraftInvariant, d.Kind)
	}
	if !d.State.IsValid() {
		return fmt.Errorf("%w: state %d", ErrDraftInvariant, d.State)
	}
	if strings.TrimSpace(d.PayloadJSON) == "" {
		return fmt.Errorf("%w: payload_json is empty", ErrDraftInvariant)
	}
	if d.CreatedAt <= 0 {
		return fmt.Errorf("%w: created_at %d must be positive", ErrDraftInvariant, d.CreatedAt)
	}
	if d.ExpiresAt != d.CreatedAt+DraftExpiryWindow {
		return fmt.Errorf("%w: expires_at %d must equal created_at+%d", ErrDraftInvariant, d.ExpiresAt, DraftExpiryWindow)
	}
	if d.State.IsTerminal() {
		if d.DecidedAt <= 0 {
			return fmt.Errorf("%w: terminal state %s missing decided_at", ErrDraftInvariant, d.State)
		}
		if d.State != DraftExpired && !d.DecidedBy.IsValid() {
			return fmt.Errorf("%w: terminal state %s missing decided_by", ErrDraftInvariant, d.State)
		}
	}
	if d.Kind == DraftKindGroupCreate && !d.TargetJID.IsZero() {
		return fmt.Errorf("%w: group_create must have zero target_jid", ErrDraftInvariant)
	}
	if d.Kind != DraftKindGroupCreate && d.TargetJID.IsZero() {
		return fmt.Errorf("%w: kind %s requires target_jid", ErrDraftInvariant, d.Kind)
	}
	return nil
}

// NewDraft constructs a pending_review draft with ExpiresAt derived from
// CreatedAt. It validates the result before returning.
func NewDraft(id, profile string, kind DraftKind, payloadJSON string, target JID, now int64) (Draft, error) {
	d := Draft{
		ID:          id,
		Profile:     profile,
		Kind:        kind,
		PayloadJSON: payloadJSON,
		TargetJID:   target,
		State:       DraftPendingReview,
		CreatedAt:   now,
		ExpiresAt:   now + DraftExpiryWindow,
	}
	if err := d.Validate(); err != nil {
		return Draft{}, err
	}
	return d, nil
}

// Approve transitions pending_review → approved. Second approve (or any
// transition from a terminal state) returns ErrDraftState.
func (d Draft) Approve(at int64, by DraftDecider) (Draft, error) {
	if d.State != DraftPendingReview {
		return d, fmt.Errorf("%w: cannot approve from %s", ErrDraftState, d.State)
	}
	if !by.IsValid() || by == DraftDeciderTimeout {
		return d, fmt.Errorf("%w: approve requires cli|skill decider, got %q", ErrDraftState, by)
	}
	if at < d.CreatedAt {
		return d, fmt.Errorf("%w: decided_at %d before created_at %d", ErrDraftInvariant, at, d.CreatedAt)
	}
	d.State = DraftApproved
	d.DecidedAt = at
	d.DecidedBy = by
	return d, nil
}

// Reject transitions pending_review → rejected, recording an optional
// human-readable reason.
func (d Draft) Reject(at int64, by DraftDecider, reason string) (Draft, error) {
	if d.State != DraftPendingReview {
		return d, fmt.Errorf("%w: cannot reject from %s", ErrDraftState, d.State)
	}
	if !by.IsValid() || by == DraftDeciderTimeout {
		return d, fmt.Errorf("%w: reject requires cli|skill decider, got %q", ErrDraftState, by)
	}
	if at < d.CreatedAt {
		return d, fmt.Errorf("%w: decided_at %d before created_at %d", ErrDraftInvariant, at, d.CreatedAt)
	}
	d.State = DraftRejected
	d.DecidedAt = at
	d.DecidedBy = by
	d.Reason = reason
	return d, nil
}

// ExpireAt transitions any non-terminal state → expired. The sweeper calls
// this once it observes at >= ExpiresAt. Terminal inputs are idempotent
// no-ops (returned unchanged, no error), which simplifies sweeper retries.
func (d Draft) ExpireAt(at int64) (Draft, error) {
	if d.State.IsTerminal() {
		return d, nil
	}
	if at < d.ExpiresAt {
		return d, fmt.Errorf("%w: cannot expire at %d before expires_at %d", ErrDraftState, at, d.ExpiresAt)
	}
	d.State = DraftExpired
	d.DecidedAt = at
	d.DecidedBy = DraftDeciderTimeout
	return d, nil
}
