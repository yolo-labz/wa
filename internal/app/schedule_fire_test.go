package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// fakeDraftStore satisfies app.DraftStore in-memory. Put is the only
// transition used by the fire-path; other methods stay as stubs.
type fakeDraftStore struct {
	mu   sync.Mutex
	rows map[string]domain.Draft
}

func newFakeDraftStore() *fakeDraftStore {
	return &fakeDraftStore{rows: make(map[string]domain.Draft)}
}

func (d *fakeDraftStore) Put(_ context.Context, dr domain.Draft) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows[dr.Profile+"|"+dr.ID] = dr
	return nil
}

func (d *fakeDraftStore) Get(_ context.Context, profile, id string) (domain.Draft, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rows[profile+"|"+id], nil
}

func (d *fakeDraftStore) List(_ context.Context, profile string, state domain.DraftState, _ int) ([]domain.Draft, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]domain.Draft, 0)
	for _, r := range d.rows {
		if r.Profile == profile && r.State == state {
			out = append(out, r)
		}
	}
	return out, nil
}

func (d *fakeDraftStore) Approve(_ context.Context, _, _ string, _ time.Time, _ domain.DraftDecider) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (d *fakeDraftStore) Reject(_ context.Context, _, _ string, _ time.Time, _ domain.DraftDecider, _ string) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (d *fakeDraftStore) ExpireDue(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

// sentSender records every Send call. Satisfies app.MessageSender.
type sentSender struct {
	mu   sync.Mutex
	sent []domain.Message
}

func (s *sentSender) Send(_ context.Context, msg domain.Message) (domain.MessageID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return domain.MessageID("sent-" + msg.To().String()), nil
}

func (s *sentSender) MarkRead(_ context.Context, _ domain.JID, _ domain.MessageID) error {
	return nil
}

func (s *sentSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// allowAll is an in-memory Allowlist that accepts everything.
type allowAll struct{}

func (allowAll) Allows(_ domain.JID, _ domain.Action) bool { return true }

// nopAudit satisfies app.AuditLog.
type nopAudit struct{}

func (nopAudit) Record(_ context.Context, _ domain.AuditEvent) error { return nil }

func buildPendingSchedule(t *testing.T, id string, kind domain.ScheduleKind, body string, fireAt, now time.Time) domain.ScheduledSend {
	t.Helper()
	jid, err := domain.Parse("5511999999999")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	mediaPath := ""
	if kind == domain.ScheduleKindSendMedia {
		mediaPath = "/tmp/media.bin"
	}
	ss, err := domain.NewScheduledSend(id, "default", kind, jid, body, mediaPath, fireAt, now)
	if err != nil {
		t.Fatalf("NewScheduledSend: %v", err)
	}
	return ss
}

// TestScheduleFireSendsTextThroughPipeline covers the direct-send happy
// path: contact not flagged for drafts → fire path calls MessageSender.
func TestScheduleFireSendsTextThroughPipeline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	drafts := newFakeDraftStore()
	sender := &sentSender{}

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	ss := buildPendingSchedule(t, "sch-1", domain.ScheduleKindSendText, "hello", now.Add(time.Hour), now)
	if err := store.Put(ctx, ss); err != nil {
		t.Fatalf("Put: %v", err)
	}

	firer := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store:  store,
		Drafts: drafts,
		Sender: sender,
		Safety: app.NewSafetyPipeline(allowAll{},
			app.NewRateLimiterAt(now.Add(-30*24*time.Hour), now)),
		Audit: nopAudit{},
		Now:   func() time.Time { return now.Add(time.Hour) },
	})

	firer.Fire(ctx, "default", "sch-1")

	if sender.count() != 1 {
		t.Fatalf("sender.count=%d want 1", sender.count())
	}
	got, err := store.Get(ctx, "default", "sch-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State() != domain.ScheduleFired {
		t.Fatalf("state=%v want fired", got.State())
	}
}

// TestScheduleToDraftContactProducesDraftAtFire is the T3-17 contract
// test: a scheduled text send whose recipient is flagged IsContact=true
// must produce a Draft row at fire time rather than sending directly.
func TestScheduleToDraftContactProducesDraftAtFire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	drafts := newFakeDraftStore()
	sender := &sentSender{}

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	ss := buildPendingSchedule(t, "sch-draft", domain.ScheduleKindSendText, "hi contact", now.Add(time.Hour), now)
	if err := store.Put(ctx, ss); err != nil {
		t.Fatalf("Put: %v", err)
	}

	firer := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store:  store,
		Drafts: drafts,
		Sender: sender,
		Safety: app.NewSafetyPipeline(allowAll{},
			app.NewRateLimiterAt(now.Add(-30*24*time.Hour), now)),
		Audit:     nopAudit{},
		Now:       func() time.Time { return now.Add(time.Hour) },
		IsContact: func(_ domain.JID) bool { return true },
	})

	firer.Fire(ctx, "default", "sch-draft")

	if sender.count() != 0 {
		t.Fatalf("sender.count=%d want 0 (draft contact must NOT direct-send)", sender.count())
	}
	// Draft row persisted in pending_review state, carrying the body.
	rows, err := drafts.List(ctx, "default", domain.DraftPendingReview, 10)
	if err != nil {
		t.Fatalf("drafts.List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("draft rows=%d want 1", len(rows))
	}
	if rows[0].Kind != domain.DraftKindSend {
		t.Fatalf("draft kind=%v want send", rows[0].Kind)
	}
	if rows[0].TargetJID.String() != ss.Recipient().String() {
		t.Fatalf("draft target=%s want %s", rows[0].TargetJID, ss.Recipient())
	}
	// Scheduled row transitioned to fired.
	got, _ := store.Get(ctx, "default", "sch-draft")
	if got.State() != domain.ScheduleFired {
		t.Fatalf("schedule state=%v want fired", got.State())
	}
}

// TestScheduleFireReloadsAfterCancel guards against the timer-fire vs
// schedule.cancel race: if the row is cancelled between arming and fire,
// the firer must not call Send nor transition state.
func TestScheduleFireReloadsAfterCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	drafts := newFakeDraftStore()
	sender := &sentSender{}

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	ss := buildPendingSchedule(t, "sch-c", domain.ScheduleKindSendText, "hi", now.Add(time.Hour), now)
	if err := store.Put(ctx, ss); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Simulate racing schedule.cancel between arming and the timer firing.
	cancelled, err := ss.MarkCancelled(now)
	if err != nil {
		t.Fatalf("MarkCancelled: %v", err)
	}
	if err := store.Update(ctx, cancelled); err != nil {
		t.Fatalf("Update: %v", err)
	}

	firer := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store:  store,
		Drafts: drafts,
		Sender: sender,
		Safety: app.NewSafetyPipeline(allowAll{},
			app.NewRateLimiterAt(now.Add(-30*24*time.Hour), now)),
		Audit: nopAudit{},
		Now:   func() time.Time { return now.Add(time.Hour) },
	})
	firer.Fire(ctx, "default", "sch-c")

	if sender.count() != 0 {
		t.Fatalf("sender.count=%d want 0 for cancelled row", sender.count())
	}
	got, _ := store.Get(ctx, "default", "sch-c")
	if got.State() != domain.ScheduleCancelled {
		t.Fatalf("state=%v want cancelled preserved", got.State())
	}
}

// TestScheduleFireMarksFailedOnSafetyDenial verifies that an allowlist
// denial at fire time transitions the row to failed (terminal) with the
// denial reason captured in LastError.
func TestScheduleFireMarksFailedOnSafetyDenial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	drafts := newFakeDraftStore()
	sender := &sentSender{}

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	ss := buildPendingSchedule(t, "sch-denied", domain.ScheduleKindSendText, "hi", now.Add(time.Hour), now)
	if err := store.Put(ctx, ss); err != nil {
		t.Fatalf("Put: %v", err)
	}

	firer := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store:  store,
		Drafts: drafts,
		Sender: sender,
		Safety: app.NewSafetyPipeline(denyAll{},
			app.NewRateLimiterAt(now.Add(-30*24*time.Hour), now)),
		Audit: nopAudit{},
		Now:   func() time.Time { return now.Add(time.Hour) },
	})
	firer.Fire(ctx, "default", "sch-denied")

	if sender.count() != 0 {
		t.Fatalf("sender.count=%d want 0 on denial", sender.count())
	}
	got, _ := store.Get(ctx, "default", "sch-denied")
	if got.State() != domain.ScheduleFailed {
		t.Fatalf("state=%v want failed", got.State())
	}
	if got.LastError() == "" {
		t.Fatalf("LastError empty")
	}
}

type denyAll struct{}

func (denyAll) Allows(_ domain.JID, _ domain.Action) bool { return false }

// TestScheduleFireRequiresSafety — T1-10 / R-03: ScheduleFirer fields are
// unexported. A test written in the app_test package cannot construct a
// firer with Safety=nil through struct init; the only construction path
// is NewScheduleFirer, which panics. This is the compile-time + runtime
// contract. (A failure to compile here means someone re-exported the
// Safety field — revert that.)
func TestScheduleFireRequiresSafety(t *testing.T) {
	t.Parallel()
	// The happy path: NewScheduleFirer with a non-nil Safety returns a
	// working firer. The coverage here is that the constructor is the
	// supported wiring surface, not struct literal.
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	firer := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store:  newFakeScheduledStore(),
		Sender: &sentSender{},
		Safety: app.NewSafetyPipeline(allowAll{},
			app.NewRateLimiterAt(now.Add(-30*24*time.Hour), now)),
		Now: func() time.Time { return now },
	})
	if firer == nil {
		t.Fatalf("NewScheduleFirer returned nil")
	}
}

// TestNilSafetyPanicsAtWiring — T1-10 / R-03: nil Safety MUST panic at
// the composition root, not silently bypass the allowlist at fire time.
// Also asserts nil Store and nil Sender panic; all three are load-bearing.
func TestNilSafetyPanicsAtWiring(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  app.ScheduleFirerConfig
		want string // substring match on panic message
	}{
		{
			name: "nil Safety",
			cfg: app.ScheduleFirerConfig{
				Store:  newFakeScheduledStore(),
				Sender: &sentSender{},
				// Safety intentionally nil
			},
			want: "Safety is required",
		},
		{
			name: "nil Store",
			cfg: app.ScheduleFirerConfig{
				Sender: &sentSender{},
				Safety: app.NewSafetyPipeline(allowAll{},
					app.NewRateLimiterAt(time.Unix(0, 0), time.Unix(0, 0))),
			},
			want: "Store is required",
		},
		{
			name: "nil Sender",
			cfg: app.ScheduleFirerConfig{
				Store: newFakeScheduledStore(),
				Safety: app.NewSafetyPipeline(allowAll{},
					app.NewRateLimiterAt(time.Unix(0, 0), time.Unix(0, 0))),
			},
			want: "Sender is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic, got nil")
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("panic payload = %T %v, want string", r, r)
				}
				if !containsSubstring(msg, tc.want) {
					t.Errorf("panic message %q does not contain %q", msg, tc.want)
				}
			}()
			_ = app.NewScheduleFirer(tc.cfg)
		})
	}
}

func containsSubstring(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
