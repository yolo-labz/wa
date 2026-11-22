package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

var fireNow = time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

// updateFailStore wraps the in-memory store: Update can be made to fail and
// every Update is counted.
type updateFailStore struct {
	*fakeScheduledStore
	updateErr error
	updates   int
}

func (s *updateFailStore) Update(ctx context.Context, ss domain.ScheduledSend) error {
	s.updates++
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.fakeScheduledStore.Update(ctx, ss)
}

type recAudit struct {
	mu   sync.Mutex
	evts []domain.AuditEvent
}

func (r *recAudit) Record(_ context.Context, e domain.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evts = append(r.evts, e)
	return nil
}

type errSender struct {
	sentSender
	err error
}

func (s *errSender) Send(ctx context.Context, m domain.Message) (domain.MessageID, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.sentSender.Send(ctx, m)
}

type failDrafts struct {
	*fakeDraftStore
	err error
}

func (d failDrafts) Put(context.Context, domain.Draft) error { return d.err }

// fireFixture builds a firer over one pending schedule of kind.
func fireFixture(t *testing.T, kind domain.ScheduleKind, cfg func(*app.ScheduleFirerConfig)) (*app.ScheduleFirer, *updateFailStore, *recAudit) {
	t.Helper()
	store := &updateFailStore{fakeScheduledStore: newFakeScheduledStore()}
	body := "hello"
	if kind == domain.ScheduleKindSendMedia {
		body = "caption"
	}
	if err := store.Put(context.Background(), buildPendingSchedule(t, "s1", kind, body, fireNow.Add(time.Hour), fireNow)); err != nil {
		t.Fatal(err)
	}
	aud := &recAudit{}
	c := app.ScheduleFirerConfig{
		Store: store, Sender: &sentSender{}, Audit: aud, Drafts: newFakeDraftStore(),
		Safety: app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
		Now:    func() time.Time { return fireNow.Add(time.Hour) },
	}
	if cfg != nil {
		cfg(&c)
	}
	return app.NewScheduleFirer(c), store, aud
}

func stateOf(t *testing.T, s *updateFailStore) domain.ScheduledSend {
	t.Helper()
	ss, err := s.Get(context.Background(), "default", "s1")
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

// TestScheduleFire_MediaGoesOutWithPathAndCaption: send_media is sent as a
// MediaMessage carrying the stored path and the body as caption, then fired
// and audited "ok".
func TestScheduleFire_MediaGoesOutWithPathAndCaption(t *testing.T) {
	t.Parallel()
	sender := &sentSender{}
	f, store, aud := fireFixture(t, domain.ScheduleKindSendMedia, func(c *app.ScheduleFirerConfig) { c.Sender = sender })
	f.Fire(context.Background(), "default", "s1")

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	m, ok := sender.sent[0].(domain.MediaMessage)
	if !ok || m.Path != "/tmp/media.bin" || m.Caption != "caption" {
		t.Fatalf("message = %#v", sender.sent[0])
	}
	if got := stateOf(t, store); got.State() != domain.ScheduleFired || !got.UpdatedAt().Equal(fireNow.Add(time.Hour)) {
		t.Errorf("state = %s at %s, want fired at the fire time", got.State(), got.UpdatedAt())
	}
	if len(aud.evts) != 1 || aud.evts[0].Decision != "ok" || aud.evts[0].Action != domain.AuditSend {
		t.Errorf("audit = %+v, want one ok send", aud.evts)
	}
}

// TestScheduleFire_ContactMediaBecomesDraftWithPayload: a media send to a
// contact is parked as a send_media draft whose payload keeps the path,
// mime and caption, with the id from NewID, and nothing is sent.
func TestScheduleFire_ContactMediaBecomesDraftWithPayload(t *testing.T) {
	t.Parallel()
	sender, drafts := &sentSender{}, newFakeDraftStore()
	f, store, _ := fireFixture(t, domain.ScheduleKindSendMedia, func(c *app.ScheduleFirerConfig) {
		c.Sender, c.Drafts = sender, drafts
		c.IsContact = func(domain.JID) bool { return true }
		c.NewID = func() string { return "draft-42" }
	})
	f.Fire(context.Background(), "default", "s1")

	if sender.count() != 0 {
		t.Fatal("a contact media send went out directly")
	}
	d, _ := drafts.Get(context.Background(), "default", "draft-42")
	var p struct{ To, Path, Mime, Caption string }
	if d.Kind != domain.DraftKindSendMedia || json.Unmarshal([]byte(d.PayloadJSON), &p) != nil ||
		p.Path != "/tmp/media.bin" || p.Caption != "caption" || p.Mime != "application/octet-stream" || p.To != d.TargetJID.String() {
		t.Fatalf("draft = %+v payload %+v", d, p)
	}
	if d.CreatedAt != fireNow.Add(time.Hour).Unix() {
		t.Errorf("CreatedAt = %d, want the fire time", d.CreatedAt)
	}
	if stateOf(t, store).State() != domain.ScheduleFired {
		t.Error("schedule not fired after parking the draft")
	}
}

// TestScheduleFire_CreateDraftKind: create_draft always parks a send draft,
// even with no IsContact hook, and the draft id defaults to the schedule id.
func TestScheduleFire_CreateDraftKind(t *testing.T) {
	t.Parallel()
	sender, drafts := &sentSender{}, newFakeDraftStore()
	f, _, _ := fireFixture(t, domain.ScheduleKindCreateDraft, func(c *app.ScheduleFirerConfig) { c.Sender, c.Drafts = sender, drafts })
	f.Fire(context.Background(), "default", "s1")

	d, _ := drafts.Get(context.Background(), "default", "s1")
	var p struct{ Body, Path string }
	if sender.count() != 0 || d.Kind != domain.DraftKindSend || json.Unmarshal([]byte(d.PayloadJSON), &p) != nil || p.Body != "hello" || p.Path != "" {
		t.Fatalf("draft = %+v payload %+v, sent %d", d, p, sender.count())
	}
}

// TestScheduleFire_FailuresAreTerminalAndAudited: every way the dispatch can
// fail ends the schedule as failed with the cause recorded and an "error"
// audit; a failing send never reports ok.
func TestScheduleFire_FailuresAreTerminalAndAudited(t *testing.T) {
	t.Parallel()
	boom := errors.New("upstream down")
	for name, tt := range map[string]struct {
		kind    domain.ScheduleKind
		cfg     func(*app.ScheduleFirerConfig)
		wantErr string
	}{
		"send error":        {domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) { c.Sender = &errSender{err: boom} }, "upstream down"},
		"media send error":  {domain.ScheduleKindSendMedia, func(c *app.ScheduleFirerConfig) { c.Sender = &errSender{err: boom} }, "upstream down"},
		"no drafts port":    {domain.ScheduleKindCreateDraft, func(c *app.ScheduleFirerConfig) { c.Drafts = nil }, "drafts port nil"},
		"draft store error": {domain.ScheduleKindCreateDraft, func(c *app.ScheduleFirerConfig) { c.Drafts = failDrafts{newFakeDraftStore(), boom} }, "persist draft"},
		"unsupported kind":  {domain.ScheduleKind(9), nil, "unsupported kind"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, store, aud := fireFixture(t, domain.ScheduleKindSendText, tt.cfg)
			if tt.kind != domain.ScheduleKindSendText {
				// Replace the row so the kind under test is what is stored.
				ss := domain.HydrateScheduledSend(domain.ScheduledSendRecord{
					ID: "s1", Profile: "default", Kind: tt.kind, To: domain.MustJID("5511999999999"),
					Body: "hello", FireAt: fireNow.Add(time.Hour), CreatedAt: fireNow, State: domain.SchedulePending, UpdatedAt: fireNow,
				})
				_ = store.Put(context.Background(), ss)
			}
			f.Fire(context.Background(), "default", "s1")
			got := stateOf(t, store)
			if got.State() != domain.ScheduleFailed || !contains(got.LastError(), tt.wantErr) {
				t.Errorf("state = %s err %q, want failed containing %q", got.State(), got.LastError(), tt.wantErr)
			}
			if len(aud.evts) != 1 || aud.evts[0].Decision != "error" || !contains(aud.evts[0].Detail, tt.wantErr) {
				t.Errorf("audit = %+v, want one error event", aud.evts)
			}
		})
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestScheduleFire_StoreFailuresDoNotPanicOrDoubleSend: an unknown row sends
// nothing; a failing Update after a successful send is logged, not retried
// (the send is not repeated and no second audit appears for a failed row).
func TestScheduleFire_StoreFailuresDoNotPanicOrDoubleSend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sender := &sentSender{}
	f, store, aud := fireFixture(t, domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) { c.Sender = sender })
	f.Fire(ctx, "default", "missing")
	if sender.count() != 0 || store.updates != 0 || len(aud.evts) != 0 {
		t.Fatalf("an unknown id had effects: sent=%d updates=%d audit=%d", sender.count(), store.updates, len(aud.evts))
	}

	store.updateErr = errors.New("disk full")
	f.Fire(ctx, "default", "s1")
	if sender.count() != 1 || store.updates != 1 {
		t.Fatalf("sent=%d updates=%d, want one send and one attempted persist", sender.count(), store.updates)
	}
	// The row is still pending (persist failed), so a second fire would
	// resend: that is the at-least-once edge the pending guard cannot close.
	if stateOf(t, store).State() != domain.SchedulePending {
		t.Error("row changed although Update failed")
	}
}

func TestScheduleFire_FailedPersistOnDenialStillAudits(t *testing.T) {
	t.Parallel()
	f, store, aud := fireFixture(t, domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) {
		c.Safety = app.NewSafetyPipeline(denyAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow))
	})
	store.updateErr = errors.New("disk full")
	f.Fire(context.Background(), "default", "s1")
	if len(aud.evts) != 1 || aud.evts[0].Decision != "error" {
		t.Errorf("audit = %+v, want the denial recorded even if the row could not be persisted", aud.evts)
	}
}

func TestNewScheduleFirer_RequiresStoreAndSender(t *testing.T) {
	t.Parallel()
	safety := app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow, fireNow))
	for name, cfg := range map[string]app.ScheduleFirerConfig{
		"nil store":  {Sender: &sentSender{}, Safety: safety},
		"nil sender": {Store: newFakeScheduledStore(), Safety: safety},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: NewScheduleFirer did not panic", name)
				}
			}()
			app.NewScheduleFirer(cfg)
		}()
	}
	// Now defaults to the wall clock: a fired row is stamped near time.Now.
	store := newFakeScheduledStore()
	ss := buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "x", fireNow.Add(time.Hour), fireNow)
	_ = store.Put(context.Background(), ss)
	app.NewScheduleFirer(app.ScheduleFirerConfig{Store: store, Sender: &sentSender{}, Safety: safety}).Fire(context.Background(), "default", "s1")
	got, _ := store.Get(context.Background(), "default", "s1")
	if got.State() != domain.ScheduleFired || time.Since(got.UpdatedAt()) > time.Minute {
		t.Errorf("default clock: state %s updated %s", got.State(), got.UpdatedAt())
	}
}
