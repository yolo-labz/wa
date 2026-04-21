package domain

import (
	"errors"
	"fmt"
	"time"
)

// ScheduledState enumerates the lifecycle states of a ScheduledSend
// (FR-110). Terminal states are fired, cancelled, failed; pending is the
// only live state.
type ScheduledState uint8

// ScheduledState values. Zero is invalid.
const (
	SchedulePending ScheduledState = iota + 1
	ScheduleFired
	ScheduleCancelled
	ScheduleFailed
)

// String returns the canonical lowercase name used on the wire.
func (s ScheduledState) String() string {
	switch s {
	case SchedulePending:
		return "pending"
	case ScheduleFired:
		return "fired"
	case ScheduleCancelled:
		return "cancelled"
	case ScheduleFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the four declared states.
func (s ScheduledState) IsValid() bool {
	return s == SchedulePending || s == ScheduleFired || s == ScheduleCancelled || s == ScheduleFailed
}

// IsTerminal reports whether s is a terminal state. Only pending is live.
func (s ScheduledState) IsTerminal() bool {
	return s == ScheduleFired || s == ScheduleCancelled || s == ScheduleFailed
}

// ScheduleKind enumerates the schedulable operation kinds. v0.5 supports
// text sends; drafts and media follow via the same state machine.
type ScheduleKind uint8

// ScheduleKind values. Zero is invalid.
const (
	ScheduleKindSendText ScheduleKind = iota + 1
	ScheduleKindSendMedia
	ScheduleKindCreateDraft
)

// IsValid reports whether k is one of the declared kinds.
func (k ScheduleKind) IsValid() bool {
	return k == ScheduleKindSendText || k == ScheduleKindSendMedia || k == ScheduleKindCreateDraft
}

// String returns the canonical lowercase name used on the wire.
func (k ScheduleKind) String() string {
	switch k {
	case ScheduleKindSendText:
		return "send_text"
	case ScheduleKindSendMedia:
		return "send_media"
	case ScheduleKindCreateDraft:
		return "create_draft"
	default:
		return "unknown"
	}
}

// ScheduledSend is the FR-110 domain entity for a deferred send operation.
// Construction validates invariants; state transitions go through the
// mutator methods and enforce the pending → {fired|cancelled|failed}
// machine with a guard against double-transition.
type ScheduledSend struct {
	id        string
	profile   string
	kind      ScheduleKind
	recipient JID
	body      string
	mediaPath string // optional; only populated when kind == send_media
	fireAt    time.Time
	createdAt time.Time
	state     ScheduledState
	updatedAt time.Time
	lastError string
}

// ErrScheduleInPast is returned when fireAt is not strictly after now.
// The socket layer maps this to JSON-RPC code -32112 per contracts/json-rpc.md.
var ErrScheduleInPast = errors.New("scheduled_send: fire_at must be strictly after now")

// ErrScheduleTerminal is returned when a transition is attempted from a
// terminal state (fired, cancelled, failed).
var ErrScheduleTerminal = errors.New("scheduled_send: cannot transition terminal state")

// NewScheduledSend constructs a pending-state ScheduledSend after
// validating the core invariants:
//   - id, profile, recipient non-zero
//   - body non-empty for text/draft kinds
//   - mediaPath non-empty for media kind
//   - fireAt strictly after now
func NewScheduledSend(id, profile string, kind ScheduleKind, to JID, body, mediaPath string, fireAt, now time.Time) (ScheduledSend, error) {
	if id == "" {
		return ScheduledSend{}, fmt.Errorf("scheduled_send: id required")
	}
	if profile == "" {
		return ScheduledSend{}, fmt.Errorf("scheduled_send: profile required")
	}
	if !kind.IsValid() {
		return ScheduledSend{}, fmt.Errorf("scheduled_send: invalid kind %d", kind)
	}
	if to.IsZero() {
		return ScheduledSend{}, fmt.Errorf("scheduled_send: recipient required")
	}
	switch kind {
	case ScheduleKindSendText, ScheduleKindCreateDraft:
		if body == "" {
			return ScheduledSend{}, ErrEmptyBody
		}
	case ScheduleKindSendMedia:
		if mediaPath == "" {
			return ScheduledSend{}, fmt.Errorf("scheduled_send: media path required for send_media")
		}
	}
	if !fireAt.After(now) {
		return ScheduledSend{}, ErrScheduleInPast
	}
	return ScheduledSend{
		id:        id,
		profile:   profile,
		kind:      kind,
		recipient: to,
		body:      body,
		mediaPath: mediaPath,
		fireAt:    fireAt.UTC(),
		createdAt: now.UTC(),
		state:     SchedulePending,
		updatedAt: now.UTC(),
	}, nil
}

// Accessors. The type is immutable from outside; transitions go through
// the mutator methods and return a new value.

// ID returns the opaque identifier chosen by the caller.
func (s ScheduledSend) ID() string { return s.id }

// Profile returns the owning profile name.
func (s ScheduledSend) Profile() string { return s.profile }

// Kind returns the operation kind.
func (s ScheduledSend) Kind() ScheduleKind { return s.kind }

// Recipient returns the destination JID.
func (s ScheduledSend) Recipient() JID { return s.recipient }

// Body returns the text body (empty for media kind).
func (s ScheduledSend) Body() string { return s.body }

// MediaPath returns the daemon-local media path (empty for non-media kinds).
func (s ScheduledSend) MediaPath() string { return s.mediaPath }

// FireAt returns the scheduled fire time in UTC.
func (s ScheduledSend) FireAt() time.Time { return s.fireAt }

// CreatedAt returns when the schedule was created.
func (s ScheduledSend) CreatedAt() time.Time { return s.createdAt }

// UpdatedAt returns the timestamp of the last state transition.
func (s ScheduledSend) UpdatedAt() time.Time { return s.updatedAt }

// State returns the current lifecycle state.
func (s ScheduledSend) State() ScheduledState { return s.state }

// LastError returns the failure message recorded by MarkFailed, or "".
func (s ScheduledSend) LastError() string { return s.lastError }

// MarkFired transitions pending → fired at the given timestamp.
// Returns ErrScheduleTerminal if already terminal.
func (s ScheduledSend) MarkFired(at time.Time) (ScheduledSend, error) {
	if s.state.IsTerminal() {
		return s, ErrScheduleTerminal
	}
	s.state = ScheduleFired
	s.updatedAt = at.UTC()
	return s, nil
}

// MarkCancelled transitions pending → cancelled at the given timestamp.
func (s ScheduledSend) MarkCancelled(at time.Time) (ScheduledSend, error) {
	if s.state.IsTerminal() {
		return s, ErrScheduleTerminal
	}
	s.state = ScheduleCancelled
	s.updatedAt = at.UTC()
	return s, nil
}

// MarkFailed transitions pending → failed at the given timestamp with a
// non-empty reason.
func (s ScheduledSend) MarkFailed(at time.Time, reason string) (ScheduledSend, error) {
	if s.state.IsTerminal() {
		return s, ErrScheduleTerminal
	}
	if reason == "" {
		reason = "unspecified"
	}
	s.state = ScheduleFailed
	s.updatedAt = at.UTC()
	s.lastError = reason
	return s, nil
}

// Reschedule mutates fireAt for a pending schedule. Terminal schedules
// cannot be rescheduled. The new fireAt must be strictly after now.
func (s ScheduledSend) Reschedule(newFireAt, now time.Time) (ScheduledSend, error) {
	if s.state.IsTerminal() {
		return s, ErrScheduleTerminal
	}
	if !newFireAt.After(now) {
		return s, ErrScheduleInPast
	}
	s.fireAt = newFireAt.UTC()
	s.updatedAt = now.UTC()
	return s, nil
}

// HydrateScheduledSend reconstructs a ScheduledSend from previously-stored
// fields WITHOUT running fire_at / body / media invariants. Stored rows
// are already validated at insertion time and may legitimately hold a past
// fireAt (pending rows resumed after downtime) or terminal state. Callers
// outside the persistence layer MUST use NewScheduledSend instead.
func HydrateScheduledSend(id, profile string, kind ScheduleKind, to JID, body, mediaPath string, fireAt, createdAt time.Time, state ScheduledState, updatedAt time.Time, lastError string) ScheduledSend {
	return ScheduledSend{
		id:        id,
		profile:   profile,
		kind:      kind,
		recipient: to,
		body:      body,
		mediaPath: mediaPath,
		fireAt:    fireAt.UTC(),
		createdAt: createdAt.UTC(),
		state:     state,
		updatedAt: updatedAt.UTC(),
		lastError: lastError,
	}
}
