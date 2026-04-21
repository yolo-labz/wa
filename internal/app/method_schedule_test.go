package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// blockingEvents is a no-op EventStream whose Next blocks until ctx is
// cancelled — lets the dispatcher's EventBridge idle cleanly in tests that
// don't care about events.
type blockingEvents struct{}

func (blockingEvents) Next(ctx context.Context) (domain.Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingEvents) Ack(_ domain.EventID) error { return nil }

// scheduleViewWire mirrors app.scheduleView (unexported) for test decoding.
type scheduleViewWire struct {
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

// newScheduleDispatcher wires a Dispatcher around the minimal ports needed
// for schedule.* method coverage.
func newScheduleDispatcher(t *testing.T, store app.ScheduledStore) *app.Dispatcher {
	t.Helper()
	d := app.NewDispatcher(app.DispatcherConfig{
		Events:         blockingEvents{},
		Allowlist:      allowAll{},
		Audit:          nopAudit{},
		Scheduled:      store,
		Profile:        "default",
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestScheduleSendPersistsAndArms covers the happy path: a valid
// schedule.send payload produces a pending row and returns the wire view.
func TestScheduleSendPersistsAndArms(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)

	raw := json.RawMessage(`{"to":"5511999999999","body":"hi","fireAt":` +
		strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`)
	got, err := d.Handle(context.Background(), "schedule.send", raw)
	if err != nil {
		t.Fatalf("schedule.send: %v", err)
	}
	var resp struct {
		Schedule scheduleViewWire `json:"schedule"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resp.Schedule.State != "pending" || resp.Schedule.Body != "hi" {
		t.Fatalf("unexpected view: %+v", resp.Schedule)
	}
	if len(store.rows) != 1 {
		t.Fatalf("store rows=%d want 1", len(store.rows))
	}
}

// TestScheduleInPast32112 covers the contract error: fire_at at or before
// now must return ErrScheduleInPast (code -32112).
func TestScheduleInPast32112(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)

	raw := json.RawMessage(`{"to":"5511999999999","body":"hi","fireAt":` +
		strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10) + `}`)
	_, err := d.Handle(context.Background(), "schedule.send", raw)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !errors.Is(err, app.ErrScheduleInPast) {
		t.Fatalf("err=%v want ErrScheduleInPast", err)
	}
	code, ok := app.IsCodedError(err)
	if !ok || code != -32112 {
		t.Fatalf("code=%d ok=%v want -32112", code, ok)
	}
	if len(store.rows) != 0 {
		t.Fatalf("past-due schedule must not persist; rows=%d", len(store.rows))
	}
}

// TestScheduleCancelTransitionsPending covers schedule.cancel on a
// pending row: returns the cancelled view and updates the store.
func TestScheduleCancelTransitionsPending(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)

	future := time.Now().Add(time.Hour).Unix()
	createRaw := json.RawMessage(`{"id":"sch-1","to":"5511999999999","body":"hi","fireAt":` +
		strconv.FormatInt(future, 10) + `}`)
	if _, err := d.Handle(context.Background(), "schedule.send", createRaw); err != nil {
		t.Fatalf("schedule.send: %v", err)
	}

	raw := json.RawMessage(`{"id":"sch-1"}`)
	got, err := d.Handle(context.Background(), "schedule.cancel", raw)
	if err != nil {
		t.Fatalf("schedule.cancel: %v", err)
	}
	var resp struct {
		Schedule scheduleViewWire `json:"schedule"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Schedule.State != "cancelled" {
		t.Fatalf("state=%q want cancelled", resp.Schedule.State)
	}
}

// TestScheduleUpdateReschedules moves a pending row to a new fire_at.
func TestScheduleUpdateReschedules(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)

	orig := time.Now().Add(time.Hour).Unix()
	createRaw := json.RawMessage(`{"id":"sch-u","to":"5511999999999","body":"hi","fireAt":` +
		strconv.FormatInt(orig, 10) + `}`)
	if _, err := d.Handle(context.Background(), "schedule.send", createRaw); err != nil {
		t.Fatalf("schedule.send: %v", err)
	}

	later := time.Now().Add(6 * time.Hour).Unix()
	upRaw := json.RawMessage(`{"id":"sch-u","fireAt":` + strconv.FormatInt(later, 10) + `}`)
	got, err := d.Handle(context.Background(), "schedule.update", upRaw)
	if err != nil {
		t.Fatalf("schedule.update: %v", err)
	}
	var resp struct {
		Schedule scheduleViewWire `json:"schedule"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Schedule.FireAt != later {
		t.Fatalf("fireAt=%d want %d", resp.Schedule.FireAt, later)
	}
}

// TestScheduleListReturnsPending covers schedule.list default behaviour:
// state="pending" filter, limit respected.
func TestScheduleListReturnsPending(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)

	ctx := context.Background()
	for i, when := range []time.Duration{time.Hour, 2 * time.Hour} {
		raw := json.RawMessage(`{"id":"sch-l-` + strconv.Itoa(i) + `","to":"5511999999999","body":"hi","fireAt":` +
			strconv.FormatInt(time.Now().Add(when).Unix(), 10) + `}`)
		if _, err := d.Handle(ctx, "schedule.send", raw); err != nil {
			t.Fatalf("schedule.send: %v", err)
		}
	}

	got, err := d.Handle(ctx, "schedule.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("schedule.list: %v", err)
	}
	var resp struct {
		Schedules []scheduleViewWire `json:"schedules"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Schedules) != 2 {
		t.Fatalf("got %d schedules, want 2", len(resp.Schedules))
	}
}

// TestScheduleSendMissingStoreReturnsMethodNotFound covers the
// zero-config case: no ScheduledStore wired → method surface absent.
func TestScheduleSendMissingStoreReturnsMethodNotFound(t *testing.T) {
	t.Parallel()
	d := newScheduleDispatcher(t, nil)
	_, err := d.Handle(context.Background(), "schedule.send",
		json.RawMessage(`{"to":"5511999999999","body":"hi","fireAt":1}`))
	if !errors.Is(err, app.ErrMethodNotFound) {
		t.Fatalf("err=%v want ErrMethodNotFound", err)
	}
}
