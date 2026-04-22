package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
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
}

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
	ss, err := domain.NewScheduledSend(id, d.profile, kind, jid, p.Body, p.MediaPath,
		time.Unix(p.FireAt, 0).UTC(), now)
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
	State string `json:"state,omitempty"` // "pending" (default), "fired", "cancelled", "failed", "" for all
	Limit int    `json:"limit,omitempty"`
}

// handleScheduleList implements "schedule.list".
func (d *Dispatcher) handleScheduleList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.scheduled == nil {
		return nil, ErrMethodNotFound
	}
	p := scheduleListParams{State: "pending", Limit: 50}
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
	state, err := parseScheduleState(p.State)
	if err != nil {
		return nil, err
	}
	rows, err := d.scheduled.List(ctx, d.profile, state, p.Limit)
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

// scheduleIDParams is the shared JSON-RPC params for "schedule.cancel"
// and partial update inputs that reference a row by id.
type scheduleIDParams struct {
	ID string `json:"id"`
}

// handleScheduleCancel implements "schedule.cancel". Idempotent: a row
// already in a terminal state returns success without modification.
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
	row, err := d.scheduled.Get(ctx, d.profile, p.ID)
	if err != nil {
		return nil, fmt.Errorf("schedule.cancel: %w", err)
	}
	if row.State().IsTerminal() {
		return marshalResult(struct {
			Schedule scheduleView `json:"schedule"`
		}{viewSchedule(row)})
	}
	cancelled, err := row.MarkCancelled(time.Now())
	if err != nil {
		return nil, fmt.Errorf("schedule.cancel: %w", err)
	}
	if err := d.scheduled.Update(ctx, cancelled); err != nil {
		return nil, fmt.Errorf("schedule.cancel: %w", err)
	}
	if d.scheduleRunner != nil {
		d.scheduleRunner.Cancel(d.profile, p.ID)
	}
	return marshalResult(struct {
		Schedule scheduleView `json:"schedule"`
	}{viewSchedule(cancelled)})
}

// scheduleUpdateParams is the JSON-RPC params for "schedule.update".
// Only fire_at is mutable on an existing pending row in v0.5.
type scheduleUpdateParams struct {
	ID     string `json:"id"`
	FireAt int64  `json:"fireAt"`
}

// handleScheduleUpdate implements "schedule.update": reschedule an existing
// pending row to a new fire_at.
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
	row, err := d.scheduled.Get(ctx, d.profile, p.ID)
	if err != nil {
		return nil, fmt.Errorf("schedule.update: %w", err)
	}
	now := time.Now()
	updated, err := row.Reschedule(time.Unix(p.FireAt, 0).UTC(), now)
	if err != nil {
		if errors.Is(err, domain.ErrScheduleInPast) {
			return nil, ErrScheduleInPast
		}
		if errors.Is(err, domain.ErrScheduleTerminal) {
			return nil, ErrInvalidParams
		}
		return nil, fmt.Errorf("schedule.update: %w", err)
	}
	if err := d.scheduled.Update(ctx, updated); err != nil {
		return nil, fmt.Errorf("schedule.update: %w", err)
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
	}
	return 0, ErrInvalidParams
}
