package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (s *updateFailStore) UpdateFrom(ctx context.Context, want domain.ScheduledState, ss domain.ScheduledSend) (bool, error) {
	s.updates++
	if s.updateErr != nil {
		return false, s.updateErr
	}
	return s.fakeScheduledStore.UpdateFrom(ctx, want, ss)
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
// nothing; a failing Update(fired) after a real send leaves the row in
// sending (claimed), so nothing can fire it again.
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
	if got := stateOf(t, store).State(); got != domain.ScheduleSending {
		t.Errorf("state after a send whose fired-persist failed = %s, want sending (never pending)", got)
	}
	f.Fire(ctx, "default", "s1")
	if sender.count() != 1 {
		t.Fatalf("a second fire re-sent: %d sends, want exactly 1", sender.count())
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

// claimFailStore refuses the claim, as a store error or as a lost race.
type claimFailStore struct {
	*fakeScheduledStore
	err error
	won bool
}

func (s claimFailStore) Claim(context.Context, domain.ScheduledSend) (bool, error) {
	return s.won, s.err
}

// TestScheduleFire_NoSendWithoutClaim: the claim precedes the send. A store
// error or a lost race (cancelled meanwhile) means nothing is sent and the
// row is left as the store has it.
func TestScheduleFire_NoSendWithoutClaim(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]claimFailStore{"store error": {err: errors.New("disk full")}, "store error with a stale win": {err: errors.New("disk full"), won: true}, "lost race": {won: false}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sender := &sentSender{}
			c.fakeScheduledStore = newFakeScheduledStore()
			_ = c.Put(context.Background(), buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow))
			f := app.NewScheduleFirer(app.ScheduleFirerConfig{
				Store: c, Sender: sender, Audit: &recAudit{}, ClaimRetryDelay: time.Millisecond,
				Safety: app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
				Now:    func() time.Time { return fireNow.Add(time.Hour) },
			})
			f.Fire(context.Background(), "default", "s1")
			if sender.count() != 0 {
				t.Fatalf("sent %d messages without holding the claim", sender.count())
			}
			if got, _ := c.Get(context.Background(), "default", "s1"); got.State() != domain.SchedulePending {
				t.Errorf("state = %s, want untouched pending", got.State())
			}
		})
	}
}

// TestScheduleFire_ClaimIsPersistedBeforeTheSend: when the send starts, the
// stored row is already sending. A send that then fails ends failed.
func TestScheduleFire_ClaimIsPersistedBeforeTheSend(t *testing.T) {
	t.Parallel()
	store := newFakeScheduledStore()
	_ = store.Put(context.Background(), buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow))
	var seen domain.ScheduledState
	sender := &probeSender{onSend: func() { r, _ := store.Get(context.Background(), "default", "s1"); seen = r.State() }}
	f := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store: store, Sender: sender,
		Safety: app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
		Now:    func() time.Time { return fireNow.Add(time.Hour) },
	})
	f.Fire(context.Background(), "default", "s1")
	if seen != domain.ScheduleSending {
		t.Errorf("state during the send = %s, want sending", seen)
	}
	if got, _ := store.Get(context.Background(), "default", "s1"); got.State() != domain.ScheduleFailed || got.LastError() != "refused" {
		t.Errorf("after a failed send: %s %q, want failed/refused", got.State(), got.LastError())
	}
}

type probeSender struct {
	sentSender
	onSend func()
}

func (p *probeSender) Send(context.Context, domain.Message) (domain.MessageID, error) {
	p.onSend()
	return "", errors.New("refused")
}

// TestScheduleRunner_InterruptedSendBecomesUncertainNotResent: a row a
// previous run left in sending is not re-armed or sent at start; it becomes
// send_uncertain (terminal) with the reason recorded, and is listed under
// that state. A pending row beside it is still armed.
func TestScheduleRunner_InterruptedSendBecomesUncertainNotResent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeScheduledStore()
	stuck := buildPendingSchedule(t, "stuck", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow)
	stuck, _ = stuck.MarkSending(fireNow.Add(time.Hour))
	_ = store.Put(ctx, stuck)
	_ = store.Put(ctx, buildPendingSchedule(t, "later", domain.ScheduleKindSendText, "hi", fireNow.Add(24*time.Hour), fireNow))

	fired := make(chan string, 4)
	r := app.NewScheduleRunner(store, "default", func(_ context.Context, _, id string) { fired <- id })
	r.Now = func() time.Time { return fireNow.Add(2 * time.Hour) }
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	got, _ := store.Get(ctx, "default", "stuck")
	if got.State() != domain.ScheduleSendUncertain || !got.State().IsTerminal() || !strings.Contains(got.LastError(), "not retried") {
		t.Errorf("stuck row = %s %q, want send_uncertain with a reason", got.State(), got.LastError())
	}
	if !got.UpdatedAt().Equal(fireNow.Add(2 * time.Hour)) {
		t.Errorf("UpdatedAt = %s, want the resolve time", got.UpdatedAt())
	}
	if r.Pending() != 1 {
		t.Errorf("armed timers = %d, want only the pending row", r.Pending())
	}
	select {
	case id := <-fired:
		t.Errorf("row %q fired at start", id)
	case <-time.After(50 * time.Millisecond):
	}
	if rows, _ := store.List(ctx, "default", domain.ScheduleSendUncertain, 10); len(rows) != 1 || rows[0].ID() != "stuck" {
		t.Errorf("uncertain list = %+v", rows)
	}
	// A second start finds nothing left to resolve.
	if rows, _ := store.List(ctx, "default", domain.ScheduleSending, 10); len(rows) != 0 {
		t.Errorf("%d rows still sending", len(rows))
	}
}

// staleGetStore serves a stale pending copy of a row another fire already
// settled: the duplicate-fire interleaving.
type staleGetStore struct {
	*fakeScheduledStore
	stale domain.ScheduledSend
}

func (s staleGetStore) Get(context.Context, string, string) (domain.ScheduledSend, error) {
	return s.stale, nil
}

// TestScheduleFire_DuplicateDeniedFireNeverOverwritesASentRow (p8 L2): a
// duplicate fire that read pending and is then denied must not write failed
// (or audit an error) over a row the first fire already sent.
func TestScheduleFire_DuplicateDeniedFireNeverOverwritesASentRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := newFakeScheduledStore()
	pending := buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow)
	sent, _ := pending.MarkSending(fireNow)
	sent, _ = sent.MarkFired(fireNow)
	_ = base.Put(ctx, sent)
	audit := &recAudit{}
	f := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store: staleGetStore{base, pending}, Sender: &sentSender{}, Audit: audit,
		Safety: app.NewSafetyPipeline(denyAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
		Now:    func() time.Time { return fireNow.Add(time.Hour) },
	})
	f.Fire(ctx, "default", "s1")
	if got, _ := base.Get(ctx, "default", "s1"); got.State() != domain.ScheduleFired || got.LastError() != "" {
		t.Errorf("row = %s %q, want fired untouched", got.State(), got.LastError())
	}
	if n := len(audit.evts); n != 0 {
		t.Errorf("%d audit events for a duplicate that changed nothing", n)
	}
}

// flakyStore fails Get and/or Claim a set number of times first.
type flakyStore struct {
	*fakeScheduledStore
	getFails, claimFails int
}

func (s *flakyStore) Get(ctx context.Context, p, id string) (domain.ScheduledSend, error) {
	if s.getFails > 0 {
		s.getFails--
		return domain.ScheduledSend{}, errors.New("database is locked")
	}
	return s.fakeScheduledStore.Get(ctx, p, id)
}

func (s *flakyStore) Claim(ctx context.Context, ss domain.ScheduledSend) (bool, error) {
	if s.claimFails > 0 {
		s.claimFails--
		return false, errors.New("database is locked")
	}
	return s.fakeScheduledStore.Claim(ctx, ss)
}

// TestScheduleFire_TransientClaimAndLoadErrorsRetryInline (p8 L1): a load or
// claim error is retried with backoff instead of waiting for a restart, sends
// exactly once when it recovers, counts every failure, and after the bounded
// attempts gives up with nothing sent and the row still pending.
func TestScheduleFire_TransientClaimAndLoadErrorsRetryInline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name                 string
		getFails, claimFails int
		wantSends            int
		wantState            domain.ScheduledState
		wantErrors           int64
	}{
		{"claim recovers on the 3rd try", 0, 2, 1, domain.ScheduleFired, 2},
		{"load and claim each fail once", 1, 1, 1, domain.ScheduleFired, 2},
		{"claim never recovers", 0, 99, 0, domain.SchedulePending, 3},
		{"load never recovers", 99, 0, 0, domain.SchedulePending, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := newFakeScheduledStore()
			_ = base.Put(ctx, buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow))
			sender := &sentSender{}
			f := app.NewScheduleFirer(app.ScheduleFirerConfig{
				Store: &flakyStore{base, tc.getFails, tc.claimFails}, Sender: sender,
				Safety:          app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
				ClaimRetryDelay: time.Millisecond, Now: func() time.Time { return fireNow.Add(time.Hour) },
			})
			f.Fire(ctx, "default", "s1")
			if sender.count() != tc.wantSends {
				t.Errorf("sends = %d, want %d", sender.count(), tc.wantSends)
			}
			if got, _ := base.Get(ctx, "default", "s1"); got.State() != tc.wantState {
				t.Errorf("state = %s, want %s", got.State(), tc.wantState)
			}
			if f.ClaimErrors() != tc.wantErrors {
				t.Errorf("schedule_claim_error_total = %d, want %d", f.ClaimErrors(), tc.wantErrors)
			}
		})
	}
}

// TestScheduleFire_RetryStopsWhenCancelled: shutdown (ctx cancelled) aborts
// the backoff wait instead of blocking Stop.
func TestScheduleFire_RetryStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	base := newFakeScheduledStore()
	_ = base.Put(context.Background(), buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow))
	f := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store: &flakyStore{base, 0, 99}, Sender: &sentSender{},
		Safety:          app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
		ClaimRetryDelay: time.Hour, Now: func() time.Time { return fireNow.Add(time.Hour) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Fire(ctx, "default", "s1"); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.ClaimErrors() == 0 && time.Now().Before(deadline) {
		<-time.After(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Fire kept waiting on the backoff after ctx was cancelled")
	}
}

// TestScheduleFire_RetriesBackOff: the waits between attempts are real and
// double (delay, 2*delay), so a stuck store is not hammered.
func TestScheduleFire_RetriesBackOff(t *testing.T) {
	t.Parallel()
	base := newFakeScheduledStore()
	_ = base.Put(context.Background(), buildPendingSchedule(t, "s1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow))
	const delay = 40 * time.Millisecond
	f := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store: &flakyStore{base, 0, 99}, Sender: &sentSender{},
		Safety:          app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
		ClaimRetryDelay: delay, Now: func() time.Time { return fireNow.Add(time.Hour) },
	})
	start := time.Now()
	f.Fire(context.Background(), "default", "s1")
	if took := time.Since(start); took < 3*delay {
		t.Errorf("3 attempts took %s, want at least %s of backoff (delay + 2*delay)", took, 3*delay)
	}
}

// TestScheduleFire_MissingRowIsNotRetried: a row that no longer exists is a
// permanent condition; Fire returns at once without counting a claim error
// (retrying cannot help and would stall the timer goroutine).
func TestScheduleFire_MissingRowIsNotRetried(t *testing.T) {
	t.Parallel()
	f := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store: newFakeScheduledStore(), Sender: &sentSender{},
		Safety:          app.NewSafetyPipeline(allowAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow)),
		ClaimRetryDelay: time.Hour, Now: func() time.Time { return fireNow },
	})
	done := make(chan struct{})
	go func() { f.Fire(context.Background(), "default", "ghost"); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Fire retried a missing row")
	}
	if f.ClaimErrors() != 0 {
		t.Errorf("schedule_claim_error_total = %d, want 0 for a missing row", f.ClaimErrors())
	}
}

// staleSendingList reports a sending row the store has since settled.
type staleSendingList struct {
	*fakeScheduledStore
	stale domain.ScheduledSend
}

func (s staleSendingList) List(ctx context.Context, p string, st domain.ScheduledState, n int) ([]domain.ScheduledSend, error) {
	if st == domain.ScheduleSending {
		return []domain.ScheduledSend{s.stale}, nil
	}
	return s.fakeScheduledStore.List(ctx, p, st, n)
}

// TestScheduleRunner_ResolveDoesNotOverwriteASettledRow: start-up resolution
// is a compare-and-set from sending; a row that finished between the list and
// the write is left alone, not rewritten to send_uncertain.
func TestScheduleRunner_ResolveDoesNotOverwriteASettledRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := newFakeScheduledStore()
	p := buildPendingSchedule(t, "r1", domain.ScheduleKindSendText, "hi", fireNow.Add(time.Hour), fireNow)
	sending, _ := p.MarkSending(fireNow)
	fired, _ := sending.MarkFired(fireNow)
	_ = base.Put(ctx, fired)
	r := app.NewScheduleRunner(staleSendingList{base, sending}, "default", func(context.Context, string, string) {})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	if got, _ := base.Get(ctx, "default", "r1"); got.State() != domain.ScheduleFired {
		t.Errorf("row = %s, want fired untouched", got.State())
	}
}

// cancelSender cancels the fire context from inside Send, as a shutdown
// mid-send does, then answers with the given result.
type cancelSender struct {
	sentSender
	cancel context.CancelFunc
	err    error
}

func (s *cancelSender) Send(ctx context.Context, m domain.Message) (domain.MessageID, error) {
	id, _ := s.sentSender.Send(ctx, m)
	s.cancel()
	return id, s.err
}

// TestScheduleFire_DispatchErrorClassifiesUncertainVersusFailed (p6 F5): an
// error that may follow the wire write (the typed ErrSendUncertain, however
// wrapped, or a context that ended mid-send) leaves a terminal send_uncertain
// row, an "uncertain" audit and exactly one send; an error proven to precede
// the wire stays failed.
func TestScheduleFire_DispatchErrorClassifiesUncertainVersusFailed(t *testing.T) {
	t.Parallel()
	ackTimeout := errors.New("ack timed out")
	for _, tc := range []struct {
		name      string
		err       error
		wantState domain.ScheduledState
		wantAudit string
		wantInErr string
	}{
		{"typed uncertain with id", domain.NewSendUncertain("3EB0ABC", ackTimeout), domain.ScheduleSendUncertain, "uncertain", "3EB0ABC"},
		{"id found through a wrapper that hides it", hiddenID{&domain.SendUncertainError{MessageID: "3EB0HID", Cause: ackTimeout}}, domain.ScheduleSendUncertain, "uncertain", "3EB0HID"},
		{"typed uncertain wrapped", fmt.Errorf("send: %w", domain.NewSendUncertain("", ackTimeout)), domain.ScheduleSendUncertain, "uncertain", "not retried"},
		{"bare sentinel", domain.ErrSendUncertain, domain.ScheduleSendUncertain, "uncertain", "not retried"},
		{"context canceled", context.Canceled, domain.ScheduleSendUncertain, "uncertain", "not retried"},
		{"context deadline", fmt.Errorf("send: %w", context.DeadlineExceeded), domain.ScheduleSendUncertain, "uncertain", "not retried"},
		{"not connected", domain.ErrDisconnected, domain.ScheduleFailed, "error", "disconnected"},
		{"validation", errors.New("body too long"), domain.ScheduleFailed, "error", "body too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sender := &errSender{err: tc.err}
			f, store, aud := fireFixture(t, domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) { c.Sender = sender })
			f.Fire(context.Background(), "default", "s1")
			got := stateOf(t, store)
			if got.State() != tc.wantState || !strings.Contains(strings.ToLower(got.LastError()), strings.ToLower(tc.wantInErr)) {
				t.Errorf("row = %s %q, want %s containing %q", got.State(), got.LastError(), tc.wantState, tc.wantInErr)
			}
			if len(aud.evts) != 1 || aud.evts[0].Decision != tc.wantAudit {
				t.Errorf("audit = %+v, want one %q event", aud.evts, tc.wantAudit)
			}
			f.Fire(context.Background(), "default", "s1")
			if sender.count() != 0 || stateOf(t, store).State() != tc.wantState {
				t.Errorf("a second fire changed the settled row (%s)", stateOf(t, store).State())
			}
		})
	}
}

// TestScheduleFire_ShutdownMidSendSettlesTheRow (F2 shape): the fire context
// ends while Send runs. The settling writes outlive it, so a send that
// returned its id is recorded fired (not left sending for the next start to
// call uncertain) and one that errored with the context is send_uncertain.
func TestScheduleFire_ShutdownMidSendSettlesTheRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		err       error
		wantState domain.ScheduledState
	}{
		{"send returned", nil, domain.ScheduleFired},
		{"send errored with the context", context.Canceled, domain.ScheduleSendUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sender := &cancelSender{cancel: cancel, err: tc.err}
			f, store, _ := fireFixture(t, domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) { c.Sender = sender })
			f.Fire(ctx, "default", "s1")
			if got := stateOf(t, store).State(); got != tc.wantState || sender.count() != 1 {
				t.Errorf("state = %s after %d sends, want %s after exactly one", got, sender.count(), tc.wantState)
			}
		})
	}
}

// hiddenID is an uncertain error whose own text does not mention the id.
type hiddenID struct{ inner *domain.SendUncertainError }

func (h hiddenID) Error() string { return "ack timed out" }
func (h hiddenID) Unwrap() error { return h.inner }

// rescheduleAfterGet moves the row's fire_at +48 h right after Get returns the
// old copy: an update that lands between the timer's load and its claim.
type rescheduleAfterGet struct{ *fakeScheduledStore }

func (r rescheduleAfterGet) Get(ctx context.Context, profile, id string) (domain.ScheduledSend, error) {
	row, err := r.fakeScheduledStore.Get(ctx, profile, id)
	if err == nil {
		later, _ := row.Reschedule(row.FireAt().Add(48*time.Hour), fireNow)
		_ = r.Update(ctx, later)
	}
	return row, err
}

// TestScheduleFire_RescheduledAfterLoadIsNotSent (p8 L1): a fire that loaded
// the row at the old time cannot claim it once an update moved fire_at, so
// nothing is sent and the row keeps the new time.
func TestScheduleFire_RescheduledAfterLoadIsNotSent(t *testing.T) {
	t.Parallel()
	sender := &sentSender{}
	f, store, _ := fireFixture(t, domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) {
		c.Sender = sender
		c.Store = rescheduleAfterGet{c.Store.(*updateFailStore).fakeScheduledStore}
	})
	f.Fire(context.Background(), "default", "s1")
	got := stateOf(t, store)
	if sender.count() != 0 || got.State() != domain.SchedulePending || !got.FireAt().After(fireNow.Add(24*time.Hour)) {
		t.Errorf("sent=%d state=%s fire_at=%s, want no send, pending, the new later time", sender.count(), got.State(), got.FireAt())
	}
}

// TestScheduleFire_DeniedFireOnAStaleCopyNeverFailsAReschedule (p8 L1, the
// pre-claim half): a fire that loaded the old time and is then denied must not
// mark a just-rescheduled pending row failed, nor audit an error.
func TestScheduleFire_DeniedFireOnAStaleCopyNeverFailsAReschedule(t *testing.T) {
	t.Parallel()
	f, store, aud := fireFixture(t, domain.ScheduleKindSendText, func(c *app.ScheduleFirerConfig) {
		c.Safety = app.NewSafetyPipeline(denyAll{}, app.NewRateLimiterAt(fireNow.Add(-30*24*time.Hour), fireNow))
		c.Store = rescheduleAfterGet{c.Store.(*updateFailStore).fakeScheduledStore}
	})
	f.Fire(context.Background(), "default", "s1")
	if got := stateOf(t, store); got.State() != domain.SchedulePending || len(aud.evts) != 0 {
		t.Errorf("state = %s, audit = %+v, want the rescheduled row pending and no audit", got.State(), aud.evts)
	}
}
