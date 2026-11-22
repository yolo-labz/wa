package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

type resolveAudit struct {
	mu     sync.Mutex
	events []domain.AuditEvent
}

func (a *resolveAudit) Record(_ context.Context, e domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

// loseOnce is a store whose first UpdateFrom loses to a concurrent resolver
// that has already written `rival`.
type loseOnce struct {
	*fakeScheduledStore
	rival domain.ScheduledSend
	done  bool
}

func (s *loseOnce) UpdateFrom(ctx context.Context, want domain.ScheduledState, ss domain.ScheduledSend) (bool, error) {
	if !s.done {
		s.done = true
		_, _ = s.fakeScheduledStore.UpdateFrom(ctx, want, s.rival)
		return false, nil
	}
	return s.fakeScheduledStore.UpdateFrom(ctx, want, ss)
}

func resolveFixture(t *testing.T, store app.ScheduledStore) (*app.Dispatcher, *resolveAudit, func(id string) domain.ScheduledSend) {
	t.Helper()
	audit := &resolveAudit{}
	d := app.NewDispatcher(app.DispatcherConfig{
		Events: blockingEvents{}, Allowlist: allowAll{}, Audit: audit, Scheduled: store,
		Profile: "default", SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
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
	return d, audit, mk
}

func uncertainRow(mk func(string) domain.ScheduledSend, id string) domain.ScheduledSend {
	s, _ := mk(id).MarkSending(time.Now())
	u, _ := s.MarkSendUncertain(time.Now(), "ack timeout")
	return u
}

func resolve(d *app.Dispatcher, id, outcome string) (scheduleViewWire, error) {
	raw, err := d.Handle(context.Background(), "schedule.resolve", json.RawMessage(`{"id":"`+id+`","outcome":"`+outcome+`"}`))
	var out struct {
		Schedule scheduleViewWire `json:"schedule"`
	}
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out.Schedule, err
}

func TestScheduleResolve_RecordsTheOperatorsWordAndAudits(t *testing.T) {
	t.Parallel()
	for outcome, want := range map[string]string{"sent": "fired", "not_sent": "failed"} {
		store := newFakeScheduledStore()
		d, audit, mk := resolveFixture(t, store)
		_ = store.Put(context.Background(), uncertainRow(mk, "u1"))
		v, err := resolve(d, "u1", outcome)
		if err != nil || v.State != want || v.Hint != "" {
			t.Fatalf("%s: view %+v err %v, want state %s without a hint", outcome, v, err, want)
		}
		if got, _ := store.Get(context.Background(), "default", "u1"); got.State().String() != want {
			t.Errorf("%s: stored state %s, want %s", outcome, got.State(), want)
		}
		if len(audit.events) != 1 || audit.events[0].Decision != "resolved_"+outcome || !strings.Contains(audit.events[0].Detail, "u1") {
			t.Errorf("%s: audit %+v, want one resolved_%s row naming u1", outcome, audit.events, outcome)
		}
		// Same outcome again: returned as is, no second audit row.
		if v, err := resolve(d, "u1", outcome); err != nil || v.State != want || len(audit.events) != 1 {
			t.Errorf("%s repeat: %+v %v, %d audit rows, want ok and 1", outcome, v, err, len(audit.events))
		}
		other := map[string]string{"sent": "not_sent", "not_sent": "sent"}[outcome]
		word := map[string]string{"fired": "sent", "failed": "failed"}[want]
		if _, err := resolve(d, "u1", other); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already "+word) {
			t.Errorf("%s then %s: %v, want ErrInvalidParams \"already %s\"", outcome, other, err, word)
		}
	}
}

func TestScheduleResolve_RefusesEverythingButAnUncertainRow(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d, audit, mk := resolveFixture(t, store)
	ctx := context.Background()
	now := time.Now()
	sending, _ := mk("sending").MarkSending(now)
	cancelled, _ := mk("cancelled").MarkCancelled(now)
	failed, _ := mk("failed").MarkFailed(now, "not allowlisted")
	firedRow, _ := mk("fired").MarkSending(now)
	firedRow, _ = firedRow.MarkFired(now)
	for _, r := range []domain.ScheduledSend{mk("pending"), sending, firedRow, cancelled, failed} {
		_ = store.Put(ctx, r)
	}
	// A row that fired by itself is not "resolved sent": both outcomes refuse it.
	if _, err := resolve(d, "fired", "not_sent"); !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("resolve not_sent on a normally fired row: %v, want ErrInvalidParams", err)
	}
	for _, id := range []string{"pending", "sending", "fired", "cancelled", "failed"} {
		before, _ := store.Get(ctx, "default", id)
		if _, err := resolve(d, id, "sent"); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already") {
			t.Errorf("resolve %s: %v, want ErrInvalidParams already <state>", id, err)
		}
		if after, _ := store.Get(ctx, "default", id); after.State() != before.State() || after.LastError() != before.LastError() {
			t.Errorf("resolve %s rewrote the row: %s -> %s", id, before.State(), after.State())
		}
	}
	for _, raw := range []string{`{"id":"","outcome":"sent"}`, `{"id":"x","outcome":"maybe"}`, `{"id":"x"}`} {
		if _, err := d.Handle(ctx, "schedule.resolve", json.RawMessage(raw)); !errors.Is(err, app.ErrInvalidParams) {
			t.Errorf("params %s: %v, want ErrInvalidParams", raw, err)
		}
	}
	if len(audit.events) != 0 {
		t.Errorf("refusals wrote %d audit rows, want 0", len(audit.events))
	}
}

// A lost compare-and-set never rewrites the row nor writes an audit row.
func TestScheduleResolve_LostCASWritesNoAudit(t *testing.T) {
	t.Parallel()
	base := newFakeScheduledStore()
	_, _, mk := resolveFixture(t, base)
	u := uncertainRow(mk, "u1")
	rivalRow, _ := u.MarkResolved(time.Now(), false)
	store := &loseOnce{fakeScheduledStore: base, rival: rivalRow}
	d, audit, _ := resolveFixture(t, store)
	_ = base.Put(context.Background(), u)
	if _, err := resolve(d, "u1", "sent"); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "already failed") {
		t.Fatalf("resolve sent after a rival not_sent won: %v, want already failed", err)
	}
	if got, _ := base.Get(context.Background(), "default", "u1"); got.State() != domain.ScheduleFailed {
		t.Errorf("rival's row was rewritten: %s", got.State())
	}
	if len(audit.events) != 0 {
		t.Errorf("the loser audited: %+v", audit.events)
	}
}

func TestScheduleResolve_UncertainRowHintNamesTheCommand(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	d, _, mk := resolveFixture(t, store)
	_ = store.Put(context.Background(), uncertainRow(mk, "u1"))
	raw, _ := d.Handle(context.Background(), "schedule.list", json.RawMessage(`{}`))
	var l struct {
		Schedules []scheduleViewWire `json:"schedules"`
	}
	_ = json.Unmarshal(raw, &l)
	if len(l.Schedules) != 1 || !strings.Contains(l.Schedules[0].Hint, "wa schedule resolve <id> --outcome sent|not_sent") {
		t.Fatalf("hint = %+v, want the resolve command", l.Schedules)
	}
	_, err := d.Handle(context.Background(), "schedule.cancel", json.RawMessage(`{"id":"u1"}`))
	if err == nil || !strings.Contains(err.Error(), "wa schedule resolve") {
		t.Errorf("cancel refusal %v, want it to name wa schedule resolve", err)
	}
}

type failingAudit struct{}

func (failingAudit) Record(context.Context, domain.AuditEvent) error { return errors.New("sink down") }

// p8 L1: the compare-and-set already won, so a failing audit sink still
// returns the success result, but the lost audit row is logged with the id.
func TestScheduleResolve_AuditFailureIsLoggedNotSwallowed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	store := newFakeScheduledStore()
	d := app.NewDispatcher(app.DispatcherConfig{
		Events: blockingEvents{}, Allowlist: allowAll{}, Audit: failingAudit{}, Scheduled: store,
		Profile: "default", SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	t.Cleanup(func() { _ = d.Close() })
	_, _, mk := resolveFixture(t, newFakeScheduledStore())
	_ = store.Put(context.Background(), uncertainRow(mk, "u1"))
	v, err := resolve(d, "u1", "sent")
	if err != nil || v.State != "fired" {
		t.Fatalf("view %+v err %v, want the row closed despite the sink", v, err)
	}
	if log := buf.String(); !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "u1") || !strings.Contains(log, "sink down") {
		t.Errorf("log %q, want an ERROR naming id u1 and the sink error", log)
	}
}

// p8 I1: an unknown id is a -32602 naming the id, not an internal error.
func TestScheduleResolve_UnknownIDIsInvalidParams(t *testing.T) {
	t.Parallel()
	d, _, _ := resolveFixture(t, newFakeScheduledStore())
	if _, err := resolve(d, "nope", "sent"); !errors.Is(err, app.ErrInvalidParams) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err %v, want ErrInvalidParams naming the id", err)
	}
}
