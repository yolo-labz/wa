package domain

import (
	"errors"
	"fmt"
	"time"
)

// ScheduledState enumerates the lifecycle states of a ScheduledSend
// (FR-110). Terminal states are fired, cancelled, failed, send_uncertain.
// pending is the only armed state; sending is the claimed-for-send state
// (at-most-once: a row found in sending after a restart is never re-sent,
// it becomes send_uncertain for the operator to resolve).
type ScheduledState uint8

// ScheduledState values. Zero is invalid.
const (
	SchedulePending ScheduledState = iota + 1
	ScheduleFired
	ScheduleCancelled
	ScheduleFailed
	ScheduleSending
	ScheduleSendUncertain
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
	case ScheduleSending:
		return "sending"
	case ScheduleSendUncertain:
		return "send_uncertain"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the declared states.
func (s ScheduledState) IsValid() bool {
	return s >= SchedulePending && s <= ScheduleSendUncertain
}

// IsTerminal reports whether s is a terminal state. pending and sending are
// the only non-terminal states.
func (s ScheduledState) IsTerminal() bool {
	return s.IsValid() && s != SchedulePending && s != ScheduleSending
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
// mutator methods and enforce the pending → sending → {fired|failed|send_uncertain} (or cancelled)
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

// ErrScheduleInFlight is returned when a row is claimed for sending
// (state sending): its outcome is not known yet, so it can be neither
// cancelled nor rescheduled.
var ErrScheduleInFlight = errors.New("scheduled_send: send in flight")

// ErrScheduleTerminal is returned when a transition is attempted from a
// terminal state (fired, cancelled, failed).
var ErrScheduleTerminal = errors.New("scheduled_send: cannot transition terminal state")

// ScheduledSendSpec is the caller-supplied input to NewScheduledSend:
// the message being scheduled plus the clock reading the fire-at
// invariant is evaluated against.
type ScheduledSendSpec struct {
	ID        string
	Profile   string
	Kind      ScheduleKind
	To        JID
	Body      string
	MediaPath string
	FireAt    time.Time
	Now       time.Time
}

// NewScheduledSend constructs a pending-state ScheduledSend after
// validating the core invariants:
//   - id, profile, recipient non-zero
//   - body non-empty for text/draft kinds
//   - mediaPath non-empty for media kind
//   - fireAt strictly after now
func NewScheduledSend(spec ScheduledSendSpec) (ScheduledSend, error) {
	// Unpack once so the invariant checks below read as plain field names.
	id, profile, kind := spec.ID, spec.Profile, spec.Kind
	to, body, mediaPath := spec.To, spec.Body, spec.MediaPath
	fireAt, now := spec.FireAt, spec.Now
	if id == "" {
		return ScheduledSend{}, errors.New("scheduled_send: id required")
	}
	if profile == "" {
		return ScheduledSend{}, errors.New("scheduled_send: profile required")
	}
	if !kind.IsValid() {
		return ScheduledSend{}, fmt.Errorf("scheduled_send: invalid kind %d", kind)
	}
	if to.IsZero() {
		return ScheduledSend{}, errors.New("scheduled_send: recipient required")
	}
	switch kind {
	case ScheduleKindSendText, ScheduleKindCreateDraft:
		if body == "" {
			return ScheduledSend{}, ErrEmptyBody
		}
	case ScheduleKindSendMedia:
		if mediaPath == "" {
			return ScheduledSend{}, errors.New("scheduled_send: media path required for send_media")
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
	if err := s.refuseUnlessPending(); err != nil {
		return s, err
	}
	s.state = ScheduleCancelled
	s.updatedAt = at.UTC()
	return s, nil
}

// refuseUnlessPending maps every non-pending state to its refusal.
func (s ScheduledSend) refuseUnlessPending() error {
	switch {
	case s.state == ScheduleSending:
		return ErrScheduleInFlight
	case s.state.IsTerminal():
		return ErrScheduleTerminal
	}
	return nil
}

// MarkSending transitions pending → sending: the claim taken immediately
// before the external send. Only a pending row can be claimed.
func (s ScheduledSend) MarkSending(at time.Time) (ScheduledSend, error) {
	if s.state != SchedulePending {
		return s, ErrScheduleTerminal
	}
	s.state = ScheduleSending
	s.updatedAt = at.UTC()
	return s, nil
}

// MarkSendUncertain transitions sending → send_uncertain: the send may or
// may not have reached WhatsApp, so it is never retried automatically.
func (s ScheduledSend) MarkSendUncertain(at time.Time, reason string) (ScheduledSend, error) {
	if s.state != ScheduleSending {
		return s, ErrScheduleTerminal
	}
	s.state = ScheduleSendUncertain
	s.updatedAt = at.UTC()
	s.lastError = reason
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
	if err := s.refuseUnlessPending(); err != nil {
		return s, err
	}
	if !newFireAt.After(now) {
		return s, ErrScheduleInPast
	}
	s.fireAt = newFireAt.UTC()
	s.updatedAt = now.UTC()
	return s, nil
}

// ScheduledSendRecord is a previously-stored ScheduledSend row.
type ScheduledSendRecord struct {
	ID        string
	Profile   string
	Kind      ScheduleKind
	To        JID
	Body      string
	MediaPath string
	FireAt    time.Time
	CreatedAt time.Time
	State     ScheduledState
	UpdatedAt time.Time
	LastError string
}

// HydrateScheduledSend reconstructs a ScheduledSend from previously-stored
// fields WITHOUT running fire_at / body / media invariants. Stored rows
// are already validated at insertion time and may legitimately hold a past
// fireAt (pending rows resumed after downtime) or terminal state. Callers
// outside the persistence layer MUST use NewScheduledSend instead.
func HydrateScheduledSend(rec ScheduledSendRecord) ScheduledSend {
	return ScheduledSend{
		id:        rec.ID,
		profile:   rec.Profile,
		kind:      rec.Kind,
		recipient: rec.To,
		body:      rec.Body,
		mediaPath: rec.MediaPath,
		fireAt:    rec.FireAt.UTC(),
		createdAt: rec.CreatedAt.UTC(),
		state:     rec.State,
		updatedAt: rec.UpdatedAt.UTC(),
		lastError: rec.LastError,
	}
}
