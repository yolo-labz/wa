package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	errScheduleCancelFmt = "schedule.cancel: %w"
	errScheduleUpdateFmt = "schedule.update: %w"
)

// scheduleView is the wire shape of a ScheduledSend row (FR-110).
type scheduleView struct {
	ID        string `json:"id"`
	Profile   string `json:"profile"`
	Kind      string `json:"kind"`
	Recipient string `json:"recipient"`
	Body      string `json:"body,omitempty"`
	MediaPath string `json:"mediaPath,omitempty"`
	FireAt    int64  `json:"fireAt"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	State     string `json:"state"`
	LastError string `json:"lastError,omitempty"`
	// Overdue marks a pending row well past its fire time: the firer gave up
	// (claim errors) and it waits for the next daemon start. Operator signal.
	Overdue bool `json:"overdue,omitempty"`
	// Hint names the operator's next step for a row the daemon will not touch.
	Hint string `json:"hint,omitempty"`
}

// uncertainNext is what an operator does with a send_uncertain row: the row
// is a closed record (cancel and update refuse it), so the only moves are to
// look and, if the message never arrived, send again as a new schedule.
const uncertainNext = "the message may have been sent: check the chat before sending again, then record what you saw with `wa schedule resolve <id> --outcome sent|not_sent`; if it did not arrive, schedule it again or send it by hand"

func scheduleHint(st domain.ScheduledState) string {
	switch st {
	case domain.ScheduleSendUncertain:
		return uncertainNext + ". It cannot be cancelled or updated"
	case domain.ScheduleSending:
		return "being sent now; if the daemon stops first it becomes send_uncertain. Not cancellable"
	}
	return ""
}

// overdueGrace is how late a pending row may be before it reads as overdue.
const overdueGrace = time.Minute

func viewSchedule(s domain.ScheduledSend) scheduleView {
	return scheduleView{
		ID:        s.ID(),
		Profile:   s.Profile(),
		Kind:      s.Kind().String(),
		Recipient: s.Recipient().String(),
		Body:      s.Body(),
		MediaPath: s.MediaPath(),
		FireAt:    s.FireAt().Unix(),
		CreatedAt: s.CreatedAt().Unix(),
		UpdatedAt: s.UpdatedAt().Unix(),
		State:     s.State().String(),
		LastError: s.LastError(),
		Overdue:   s.State() == domain.SchedulePending && time.Since(s.FireAt()) > overdueGrace,
		Hint:      scheduleHint(s.State()),
	}
}

// scheduleSendParams is the JSON-RPC params for "schedule.send". All
// fields are user-supplied; id defaults to a fresh ULID.
type scheduleSendParams struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind,omitempty"` // "send_text" (default), "send_media", "create_draft"
	To        string `json:"to"`
	Body      string `json:"body,omitempty"`
	MediaPath string `json:"mediaPath,omitempty"`
	FireAt    int64  `json:"fireAt"` // unix seconds
}

// handleScheduleSend implements "schedule.send". Validates params,
// constructs the pending domain entity, persists it, then arms the timer
// via ScheduleRunner (no-op if runner is nil). Wrapped in the FR-034a
// idempotency sidecar.
func (d *Dispatcher) handleScheduleSend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "schedule.send", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doScheduleSend(ctx, raw)
	})
}

func (d *Dispatcher) doScheduleSend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.scheduled == nil {
		return nil, ErrMethodNotFound
	}
	var p scheduleSendParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.FireAt <= 0 {
		return nil, ErrInvalidParams
	}
	mediaPath, err := d.confinePath(ctx, "schedule.send", p.MediaPath)
	if err != nil {
		return nil, err
	}
	p.MediaPath = mediaPath
	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}
	kind, err := parseScheduleKind(p.Kind)
	if err != nil {
		return nil, err
	}
	id := p.ID
	if id == "" {
		id = newScheduleID()
	}
	now := time.Now()
	ss, err := domain.NewScheduledSend(domain.ScheduledSendSpec{
		ID: id, Profile: d.profile, Kind: kind, To: jid, Body: p.Body, MediaPath: p.MediaPath,
		FireAt: time.Unix(p.FireAt, 0).UTC(), Now: now,
	})
	if err != nil {
		if errors.Is(err, domain.ErrScheduleInPast) {
			return nil, ErrScheduleInPast
		}
		if errors.Is(err, domain.ErrEmptyBody) {
			return nil, ErrInvalidParams
		}
		return nil, fmt.Errorf("schedule.send: %w", err)
	}
	if err := d.scheduled.Put(ctx, ss); err != nil {
		return nil, fmt.Errorf("schedule.send: %w", err)
	}
	if d.scheduleRunner != nil {
		d.scheduleRunner.Schedule(ss)
	}
	return marshalResult(struct {
		Schedule scheduleView `json:"schedule"`
	}{viewSchedule(ss)})
}

// scheduleListParams is the JSON-RPC params for "schedule.list".
type scheduleListParams struct {
	State string `json:"state,omitempty"` // "open" (default: pending, sending, send_uncertain), "pending", "fired", "cancelled", "failed", "sending", "send_uncertain", "" for all
	Limit int    `json:"limit,omitempty"`
}

// handleScheduleList implements "schedule.list".
func (d *Dispatcher) handleScheduleList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.scheduled == nil {
		return nil, ErrMethodNotFound
	}
	p := scheduleListParams{State: scheduleStateOpen, Limit: 50}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	rows, err := d.listSchedules(ctx, p.State, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("schedule.list: %w", err)
	}
	out := make([]scheduleView, 0, len(rows))
	for _, r := range rows {
		out = append(out, viewSchedule(r))
	}
	return marshalResult(struct {
		Schedules []scheduleView `json:"schedules"`
	}{out})
}

// scheduleStateOpen is the default schedule.list filter.
const scheduleStateOpen = "open"

// listSchedules resolves the filter; "open" lists send_uncertain, sending,
// then pending, so a long pending queue never pushes the first two out.
func (d *Dispatcher) listSchedules(ctx context.Context, filter string, limit int) ([]domain.ScheduledSend, error) {
	if filter != scheduleStateOpen {
		state, err := parseScheduleState(filter)
		if err != nil {
			return nil, err
		}
		return d.scheduled.List(ctx, d.profile, state, limit)
	}
	var out []domain.ScheduledSend
	for _, st := range []domain.ScheduledState{domain.ScheduleSendUncertain, domain.ScheduleSending, domain.SchedulePending} {
		rows, err := d.scheduled.List(ctx, d.profile, st, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// scheduleIDParams is the shared JSON-RPC params for "schedule.cancel"
// and partial update inputs that reference a row by id.
type scheduleIDParams struct {
	ID string `json:"id"`
}

// handleScheduleCancel implements "schedule.cancel". Cancelling an already
// cancelled row is idempotent; a row that is sending, sent, failed or
// uncertain answers -32602 "already <state>" and is never rewritten.
func (d *Dispatcher) handleScheduleCancel(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.scheduled == nil {
		return nil, ErrMethodNotFound
	}
	var p scheduleIDParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	cancelled, err := d.casFromPending(ctx, p.ID, errScheduleCancelFmt, domain.ScheduleCancelled,
		func(row domain.ScheduledSend) (domain.ScheduledSend, error) { return row.MarkCancelled(time.Now()) })
	if err != nil {
		return nil, err
	}
	if d.scheduleRunner != nil {
		d.scheduleRunner.Cancel(d.profile, p.ID)
	}
	return marshalResult(struct {
		Schedule scheduleView `json:"schedule"`
	}{viewSchedule(cancelled)})
}

// scheduleResolveParams is the JSON-RPC params for "schedule.resolve".
type scheduleResolveParams struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"` // "sent" | "not_sent"
}

// handleScheduleResolve implements "schedule.resolve": the operator's record
// of what the chat showed for a send_uncertain row. It sends nothing. The
// row becomes fired (sent) or failed (not_sent) by compare-and-set; the same
// outcome again is returned as is, the other one is refused, and only the
// winner of the compare-and-set writes the audit row.
func (d *Dispatcher) handleScheduleResolve(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.scheduled == nil {
		return nil, ErrMethodNotFound
	}
	var p scheduleResolveParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	sent := p.Outcome == "sent"
	if p.ID == "" || (!sent && p.Outcome != "not_sent") {
		return nil, ErrInvalidParams
	}
	row, err := d.scheduled.Get(ctx, d.profile, p.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("schedule.resolve: no schedule %q: %w", p.ID, ErrInvalidParams)
	}
	if err != nil {
		return nil, resolveErr(err)
	}
	if next, merr := row.MarkResolved(time.Now(), sent); merr == nil {
		won, uerr := d.scheduled.UpdateFrom(ctx, domain.ScheduleSendUncertain, next)
		if uerr != nil {
			return nil, resolveErr(uerr)
		}
		if won {
			d.auditResolved(ctx, next, p.Outcome)
			return scheduleResult(next)
		}
		if row, err = d.scheduled.Get(ctx, d.profile, p.ID); err != nil {
			return nil, resolveErr(err)
		}
	}
	if resolvedAs(row, sent) {
		return scheduleResult(row)
	}
	return nil, errScheduleStateChanged(row.State())
}

func resolveErr(err error) error { return fmt.Errorf("schedule.resolve: %w", err) }

func scheduleResult(row domain.ScheduledSend) (json.RawMessage, error) {
	return marshalResult(struct {
		Schedule scheduleView `json:"schedule"`
	}{viewSchedule(row)})
}

// resolvedAs reports whether row was already closed by an operator with the same outcome.
func resolvedAs(row domain.ScheduledSend, sent bool) bool {
	word := "not sent"
	if sent {
		word = "sent"
	}
	return strings.HasPrefix(row.LastError(), domain.ResolvedNote) && strings.Contains(row.LastError(), ": "+word+" (")
}

// auditResolved records the resolution. The compare-and-set already won, so a
// failing sink cannot undo the close: it is logged loudly (id and outcome) and
// the caller still gets the success result.
func (d *Dispatcher) auditResolved(ctx context.Context, row domain.ScheduledSend, outcome string) {
	if d.audit == nil {
		return
	}
	if err := d.audit.Record(ctx, domain.NewAuditEvent("schedule", domain.AuditSend, row.Recipient(), "resolved_"+outcome, "id="+row.ID())); err != nil {
		d.log.Error("schedule.resolve: audit row not written", "id", row.ID(), "outcome", outcome, "err", err)
	}
}

// casFromPending applies build to the row only while it is still pending, as
// a compare-and-set against the store. A row already in state okState is
// returned as is (idempotent repeat). Any other state, read up front or after
// losing the race to a claim, is refused with errScheduleStateChanged and the
// stored row is left exactly as it is.
func (d *Dispatcher) casFromPending(ctx context.Context, id, errFmt string, okState domain.ScheduledState,
	build func(domain.ScheduledSend) (domain.ScheduledSend, error),
) (domain.ScheduledSend, error) {
	row, err := d.scheduled.Get(ctx, d.profile, id)
	if err != nil {
		return row, fmt.Errorf(errFmt, err)
	}
	if row.State() == domain.SchedulePending {
		next, err := build(row)
		if err != nil {
			return row, err
		}
		won, err := d.scheduled.UpdateFrom(ctx, domain.SchedulePending, next)
		if err != nil {
			return row, fmt.Errorf(errFmt, err)
		}
		if won {
			return next, nil
		}
		if row, err = d.scheduled.Get(ctx, d.profile, id); err != nil {
			return row, fmt.Errorf(errFmt, err)
		}
	}
	if row.State() == okState {
		return row, nil
	}
	return row, errScheduleStateChanged(row.State())
}

// errScheduleStateChanged is the typed refusal for a transition whose row is
// no longer pending: -32602 with the state the operator needs to see.
func errScheduleStateChanged(st domain.ScheduledState) error {
	word := st.String()
	if st == domain.ScheduleFired {
		word = "sent"
	}
	if st == domain.ScheduleSendUncertain {
		word += ": " + uncertainNext
	}
	return fmt.Errorf("already %s: %w", word, ErrInvalidParams)
}

// scheduleUpdateParams is the JSON-RPC params for "schedule.update".
// Only fire_at is mutable on an existing pending row in v0.5.
type scheduleUpdateParams struct {
	ID     string `json:"id"`
	FireAt int64  `json:"fireAt"`
}

// handleScheduleUpdate implements "schedule.update": reschedule an existing
// pending row to a new fire_at (compare-and-set, see casFromPending).
func (d *Dispatcher) handleScheduleUpdate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.scheduled == nil {
		return nil, ErrMethodNotFound
	}
	var p scheduleUpdateParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" || p.FireAt <= 0 {
		return nil, ErrInvalidParams
	}
	updated, err := d.casFromPending(ctx, p.ID, errScheduleUpdateFmt, 0,
		func(row domain.ScheduledSend) (domain.ScheduledSend, error) {
			next, err := row.Reschedule(time.Unix(p.FireAt, 0).UTC(), time.Now())
			if errors.Is(err, domain.ErrScheduleInPast) {
				return row, ErrScheduleInPast
			}
			if err != nil {
				return row, fmt.Errorf(errScheduleUpdateFmt, err)
			}
			return next, nil
		})
	if err != nil {
		return nil, err
	}
	if d.scheduleRunner != nil {
		d.scheduleRunner.Schedule(updated)
	}
	return marshalResult(struct {
		Schedule scheduleView `json:"schedule"`
	}{viewSchedule(updated)})
}

// newScheduleID mints a random 128-bit id encoded as 32 hex chars. Used
// when the caller omits an id; collisions are astronomically unlikely.
func newScheduleID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand can only fail on broken platforms; fall back to
		// timestamp so schedule.send still succeeds.
		return fmt.Sprintf("sch-%x", time.Now().UnixNano())
	}
	return "sch-" + hex.EncodeToString(b[:])
}

func parseScheduleKind(s string) (domain.ScheduleKind, error) {
	switch s {
	case "", "send_text":
		return domain.ScheduleKindSendText, nil
	case "send_media":
		return domain.ScheduleKindSendMedia, nil
	case "create_draft":
		return domain.ScheduleKindCreateDraft, nil
	}
	return 0, ErrInvalidParams
}

func parseScheduleState(s string) (domain.ScheduledState, error) {
	switch s {
	case "":
		return 0, nil // all states
	case "pending":
		return domain.SchedulePending, nil
	case "fired":
		return domain.ScheduleFired, nil
	case "cancelled":
		return domain.ScheduleCancelled, nil
	case "failed":
		return domain.ScheduleFailed, nil
	case "sending":
		return domain.ScheduleSending, nil
	case "send_uncertain":
		return domain.ScheduleSendUncertain, nil
	}
	return 0, ErrInvalidParams
}
