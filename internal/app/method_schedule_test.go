package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
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

func (blockingEvents) ResumeFrom(_ uint64) error { return nil }

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
	Overdue   bool   `json:"overdue,omitempty"`
	Hint      string `json:"hint,omitempty"`
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

// TestScheduleInFlightRowsAreVisibleAndProtected: a row claimed for sending
// is refused by cancel and update (-32602, never silently changed) and a
// row in send_uncertain is listed by its own state filter, so the operator
// can see and resolve what the daemon refused to re-send.
func TestScheduleInFlightRowsAreVisibleAndProtected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)
	now := time.Now()
	mk := func(id string) domain.ScheduledSend {
		ss, err := domain.NewScheduledSend(domain.ScheduledSendSpec{
			ID: id, Profile: "default", Kind: domain.ScheduleKindSendText,
			To: domain.MustJID("5511999999999"), Body: "hi", FireAt: now.Add(time.Hour), Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return ss
	}
	mkPending := func(id string, at time.Time) domain.ScheduledSend { return mk(id) }
	sending, _ := mk("in-flight").MarkSending(now)
	uncertain, _ := mk("lost").MarkSending(now)
	uncertain, _ = uncertain.MarkSendUncertain(now, "daemon stopped while sending")
	_ = store.Put(ctx, sending)
	_ = store.Put(ctx, uncertain)

	if _, err := d.Handle(ctx, "schedule.cancel", json.RawMessage(`{"id":"in-flight"}`)); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already sending") {
		t.Errorf("cancel of a sending row: %v, want ErrInvalidParams \"already sending\"", err)
	}
	up := json.RawMessage(`{"id":"in-flight","fireAt":` + strconv.FormatInt(now.Add(5*time.Hour).Unix(), 10) + `}`)
	if _, err := d.Handle(ctx, "schedule.update", up); !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("update of a sending row: %v, want ErrInvalidParams", err)
	}
	if got, _ := store.Get(ctx, "default", "in-flight"); got.State() != domain.ScheduleSending {
		t.Errorf("a refused cancel/update changed the row to %s", got.State())
	}
	// A send_uncertain row is not cancellable either, and stays as it is.
	if _, err := d.Handle(ctx, "schedule.cancel", json.RawMessage(`{"id":"lost"}`)); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already send_uncertain") {
		t.Errorf("cancel of an uncertain row: %v, want \"already send_uncertain\"", err)
	}
	if got, _ := store.Get(ctx, "default", "lost"); got.State() != domain.ScheduleSendUncertain {
		t.Errorf("uncertain row became %s", got.State())
	}
	// The refusal and the list row both say what to do next (check the chat).
	_, err := d.Handle(ctx, "schedule.cancel", json.RawMessage(`{"id":"lost"}`))
	if err == nil || !strings.Contains(err.Error(), "check the chat") {
		t.Errorf("uncertain cancel refusal %v lacks the next step", err)
	}
	_ = store.Put(ctx, mkPending("plain", now))
	fired, _ := mk("done").MarkSending(now)
	fired, _ = fired.MarkFired(now)
	_ = store.Put(ctx, fired)
	for id, want := range map[string]string{"lost": "check the chat", "in-flight": "becomes send_uncertain"} {
		raw, _ := d.Handle(ctx, "schedule.list", json.RawMessage(`{"state":""}`))
		var l struct {
			Schedules []scheduleViewWire `json:"schedules"`
		}
		_ = json.Unmarshal(raw, &l)
		for _, v := range l.Schedules {
			if v.ID == id && !strings.Contains(v.Hint, want) {
				t.Errorf("%s hint = %q, want %q", id, v.Hint, want)
			}
			if v.ID != "lost" && v.ID != "in-flight" && v.Hint != "" {
				t.Errorf("%s (%s) carries a hint: %q", v.ID, v.State, v.Hint)
			}
		}
	}
	// Cancelling an already cancelled row is the one idempotent repeat.
	cn, _ := mkPending("c1", now).MarkCancelled(now)
	_ = store.Put(ctx, cn)
	if raw, err := d.Handle(ctx, "schedule.cancel", json.RawMessage(`{"id":"c1"}`)); err != nil || !strings.Contains(string(raw), `"cancelled"`) {
		t.Errorf("repeat cancel = %s, %v; want the cancelled row", raw, err)
	}
	for state, wantID := range map[string]string{"send_uncertain": "lost", "sending": "in-flight"} {
		raw, err := d.Handle(ctx, "schedule.list", json.RawMessage(`{"state":"`+state+`"}`))
		var l struct {
			Schedules []scheduleViewWire `json:"schedules"`
		}
		if err != nil || json.Unmarshal(raw, &l) != nil || len(l.Schedules) != 1 || l.Schedules[0].ID != wantID || l.Schedules[0].State != state {
			t.Errorf("list %s = %s, %v; want only %s", state, raw, err, wantID)
		}
	}
}

// racingStore claims the row right after Get returns it as pending: the
// exact interleaving where a cancel/update read pending and the timer's
// Claim lands before the write.
type racingStore struct {
	*fakeScheduledStore
	claimAfterGet bool
	settle        bool // after Get, finish the send: row ends fired instead of sending
}

func (r racingStore) Get(ctx context.Context, profile, id string) (domain.ScheduledSend, error) {
	row, err := r.fakeScheduledStore.Get(ctx, profile, id)
	if err == nil && r.claimAfterGet && row.State() == domain.SchedulePending {
		c, _ := row.MarkSending(time.Now())
		_, _ = r.Claim(ctx, c)
		if r.settle {
			f, _ := c.MarkFired(time.Now())
			_ = r.Update(ctx, f)
		}
	}
	return row, err
}

// TestScheduleCancelAndUpdateCannotOverwriteAClaim: a cancel or update that
// read pending but lost the race to the claim must fail with -32602 and
// leave the row sending, so the send is neither hidden nor repeated.
func TestScheduleCancelAndUpdateCannotOverwriteAClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct{ method, params string }{
		{"schedule.cancel", `{"id":"r1"}`},
		{"schedule.update", `{"id":"r1","fireAt":` + strconv.FormatInt(time.Now().Add(9*time.Hour).Unix(), 10) + `}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			base := newFakeScheduledStore()
			d := newScheduleDispatcher(t, racingStore{fakeScheduledStore: base, claimAfterGet: true})
			now := time.Now()
			ss, _ := domain.NewScheduledSend(domain.ScheduledSendSpec{
				ID: "r1", Profile: "default", Kind: domain.ScheduleKindSendText,
				To: domain.MustJID("5511999999999"), Body: "hi", FireAt: now.Add(time.Hour), Now: now,
			})
			_ = base.Put(ctx, ss)
			if _, err := d.Handle(ctx, tc.method, json.RawMessage(tc.params)); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already sending") {
				t.Errorf("err = %v, want ErrInvalidParams \"already sending\"", err)
			}
			if got, _ := base.Get(ctx, "default", "r1"); got.State() != domain.ScheduleSending {
				t.Errorf("row = %s, want sending (the claim must survive)", got.State())
			}
		})
	}
}

// TestScheduleListDefaultShowsOpenRows: the default filter shows pending,
// sending and send_uncertain (uncertain first, and never pushed out by a
// long pending queue under the limit); fired rows stay out; explicit
// filters still narrow.
func TestScheduleListDefaultShowsOpenRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)
	now := time.Now()
	mk := func(id string) domain.ScheduledSend {
		ss, _ := domain.NewScheduledSend(domain.ScheduledSendSpec{
			ID: id, Profile: "default", Kind: domain.ScheduleKindSendText,
			To: domain.MustJID("5511999999999"), Body: "hi", FireAt: now.Add(time.Hour), Now: now,
		})
		return ss
	}
	for _, id := range []string{"p1", "p2", "p3"} {
		_ = store.Put(ctx, mk(id))
	}
	s, _ := mk("s1").MarkSending(now)
	u, _ := mk("u1").MarkSending(now)
	u, _ = u.MarkSendUncertain(now, "x")
	f, _ := mk("f1").MarkFired(now)
	for _, r := range []domain.ScheduledSend{s, u, f} {
		_ = store.Put(ctx, r)
	}
	ids := func(params string) []string {
		raw, err := d.Handle(ctx, "schedule.list", json.RawMessage(params))
		var l struct {
			Schedules []scheduleViewWire `json:"schedules"`
		}
		if err != nil || json.Unmarshal(raw, &l) != nil {
			t.Fatalf("list %s: %v", params, err)
		}
		out := make([]string, 0, len(l.Schedules))
		for _, v := range l.Schedules {
			out = append(out, v.ID)
		}
		return out
	}
	if got := ids(`{}`); len(got) != 5 || got[0] != "u1" || got[1] != "s1" || slices.Contains(got, "f1") {
		t.Errorf("default list = %v, want u1,s1 then the 3 pending, no fired", got)
	}
	if got := ids(`{"limit":2}`); len(got) != 2 || got[0] != "u1" || got[1] != "s1" {
		t.Errorf("limit 2 = %v, want the uncertain and sending rows first", got)
	}
	if got := ids(`{"limit":4}`); len(got) != 4 {
		t.Errorf("limit 4 = %v, want exactly 4 rows (limit shared across the open states)", got)
	}
	if got := ids(`{"state":"pending"}`); len(got) != 3 {
		t.Errorf("explicit pending = %v, want 3", got)
	}
	if got := ids(`{"state":"fired"}`); len(got) != 1 || got[0] != "f1" {
		t.Errorf("explicit fired = %v", got)
	}
}

// TestScheduleLostRaceAgainstASettledRow: when the row was claimed AND
// finished between the read and the write, cancel answers with the settled
// row (cancel answers "already sent", never "cancelled"; update is refused),
// and neither rewrites it nor re-arms a timer: this is p8's stale-update probe.
func TestScheduleLostRaceAgainstASettledRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		method, params string
	}{
		{"schedule.cancel", `{"id":"r1"}`},
		{"schedule.update", `{"id":"r1","fireAt":` + strconv.FormatInt(time.Now().Add(9*time.Hour).Unix(), 10) + `}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			base := newFakeScheduledStore()
			d := newScheduleDispatcher(t, racingStore{fakeScheduledStore: base, claimAfterGet: true, settle: true})
			now := time.Now()
			ss, _ := domain.NewScheduledSend(domain.ScheduledSendSpec{
				ID: "r1", Profile: "default", Kind: domain.ScheduleKindSendText,
				To: domain.MustJID("5511999999999"), Body: "hi", FireAt: now.Add(time.Hour), Now: now,
			})
			_ = base.Put(ctx, ss)
			raw, err := d.Handle(ctx, tc.method, json.RawMessage(tc.params))
			if !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already sent") {
				t.Errorf("err = %v (%s), want ErrInvalidParams \"already sent\"", err, raw)
			}
			if got, _ := base.Get(ctx, "default", "r1"); got.State() != domain.ScheduleFired {
				t.Errorf("row = %s, want fired untouched", got.State())
			}
		})
	}
}

// TestScheduleUpdateToThePastIsRefused: a pending row cannot be moved into
// the past; the typed schedule-in-past error comes back and the row stays put.
func TestScheduleUpdateToThePastIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)
	create := json.RawMessage(`{"id":"p1","to":"5511999999999","body":"hi","fireAt":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`)
	if _, err := d.Handle(ctx, "schedule.send", create); err != nil {
		t.Fatal(err)
	}
	past := json.RawMessage(`{"id":"p1","fireAt":` + strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10) + `}`)
	if _, err := d.Handle(ctx, "schedule.update", past); !errors.Is(err, app.ErrScheduleInPast) {
		t.Errorf("err = %v, want ErrScheduleInPast", err)
	}
	if got, _ := store.Get(ctx, "default", "p1"); got.FireAt().Before(time.Now()) {
		t.Errorf("row was moved into the past: %s", got.FireAt())
	}
}

// TestScheduleListOpenCutsTheFarthestPendingAtTheLimit (p8 L2) and
// TestScheduleListMarksOverduePendingRows (p8 L3).
func TestScheduleListOpenCutsTheFarthestPendingAtTheLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)
	now := time.Now()
	for i := 5; i >= 1; i-- { // inserted farthest first
		ss, _ := domain.NewScheduledSend(domain.ScheduledSendSpec{
			ID: fmt.Sprintf("p%d", i), Profile: "default",
			Kind: domain.ScheduleKindSendText, To: domain.MustJID("5511999999999"), Body: "hi", FireAt: now.Add(time.Duration(i) * time.Hour), Now: now,
		})
		_ = store.Put(ctx, ss)
	}
	for _, params := range []string{`{"limit":3}`, `{"limit":3,"state":"pending"}`} {
		raw, _ := d.Handle(ctx, "schedule.list", json.RawMessage(params))
		var l struct {
			Schedules []scheduleViewWire `json:"schedules"`
		}
		_ = json.Unmarshal(raw, &l)
		got := make([]string, 0, len(l.Schedules))
		for _, v := range l.Schedules {
			got = append(got, v.ID)
		}
		if !slices.Equal(got, []string{"p1", "p2", "p3"}) {
			t.Errorf("list %s = %v, want the three soonest-due p1,p2,p3", params, got)
		}
	}
}

func TestScheduleListMarksOverduePendingRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	d := newScheduleDispatcher(t, store)
	now := time.Now()
	for id, fireAt := range map[string]time.Time{"late": now.Add(-time.Hour), "soon": now.Add(-10 * time.Second), "later": now.Add(time.Hour)} {
		ss, _ := domain.NewScheduledSend(domain.ScheduledSendSpec{
			ID: id, Profile: "default", Kind: domain.ScheduleKindSendText,
			To: domain.MustJID("5511999999999"), Body: "hi", FireAt: fireAt, Now: fireAt.Add(-time.Hour),
		})
		_ = store.Put(ctx, ss)
	}
	busy, _ := domain.NewScheduledSend(domain.ScheduledSendSpec{
		ID: "busy", Profile: "default", Kind: domain.ScheduleKindSendText,
		To: domain.MustJID("5511999999999"), Body: "hi", FireAt: now.Add(-time.Hour), Now: now.Add(-2 * time.Hour),
	})
	busy, _ = busy.MarkSending(now) // past its time but claimed: not overdue
	_ = store.Put(ctx, busy)
	raw, _ := d.Handle(ctx, "schedule.list", json.RawMessage(`{}`))
	var l struct {
		Schedules []scheduleViewWire `json:"schedules"`
	}
	_ = json.Unmarshal(raw, &l)
	for _, v := range l.Schedules {
		if want := v.ID == "late"; v.Overdue != want {
			t.Errorf("%s overdue = %v, want %v (a row a few seconds late is just being fired)", v.ID, v.Overdue, want)
		}
	}
}
