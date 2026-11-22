package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ScheduleFirer wires a ScheduledSend's FireFunc callback to the full
// outbound safety pipeline (FR-003, FR-112): re-load the row, run allowlist
// + rate-limit + warmup checks, then dispatch by kind — direct send for
// send_text/send_media, or a draft row for create_draft. Terminal state
// transitions (fired / failed) are persisted via ScheduledStore.Update so
// a subsequent daemon restart does not retry the row.
//
// Delivery is at-most-once. Fire first claims the row (pending → sending,
// an atomic compare-and-set) and only then sends. A crash or a failed
// persist after a real send therefore leaves the row in sending, never in
// pending: the runner does not re-arm it, and at the next start it becomes
// send_uncertain for the operator to resolve. A send is never silently
// repeated.
//
// Feature 018 T1-10 / R-03: the Safety pipeline is a load-bearing dep.
// The historic nil-Safety bypass let a mis-wired composition root skip
// allowlist + rate-limit at fire time. Fields are unexported and
// construction goes through NewScheduleFirer, which panics on nil
// Safety so drift is caught at daemon boot, not at first fire.
type ScheduleFirer struct {
	store     ScheduledStore
	drafts    DraftStore
	sender    MessageSender
	safety    *SafetyPipeline
	audit     AuditLog
	log       *slog.Logger
	now       func() time.Time
	newID     func() string         // draft-id generator; defaults to ULID
	isContact func(domain.JID) bool // optional: when true, send_text is routed to a draft

	retryDelay  time.Duration // first backoff between claim/load retries
	claimErrors atomic.Int64  // schedule_claim_error_total
}

// claimAttempts bounds the inline retries of a failed load or claim. Both are
// safe to repeat: a failed claim sent nothing, and a repeated claim that
// "won" unseen loses (no send), leaving the row for send_uncertain.
const claimAttempts = 3

// settleTimeout bounds the writes that follow a claimed send.
const settleTimeout = 5 * time.Second

// ClaimErrors is the running schedule_claim_error_total.
func (f *ScheduleFirer) ClaimErrors() int64 { return f.claimErrors.Load() }

// ScheduleFirerConfig is the set of dependencies NewScheduleFirer wires.
// Required: Store, Sender, Safety. Optional: Drafts (needed only if any
// schedule ever fires as a draft), Audit, Log, Now (defaults to time.Now),
// NewID, IsContact.
type ScheduleFirerConfig struct {
	Store     ScheduledStore
	Drafts    DraftStore
	Sender    MessageSender
	Safety    *SafetyPipeline
	Audit     AuditLog
	Log       *slog.Logger
	Now       func() time.Time
	NewID     func() string
	IsContact func(domain.JID) bool
	// ClaimRetryDelay is the first backoff (doubling) between retries of a
	// failed load/claim; defaults to 1 s.
	ClaimRetryDelay time.Duration
}

// NewScheduleFirer constructs a ScheduleFirer from cfg. Panics on nil
// Safety, Store, or Sender — these three are load-bearing and a nil
// at wiring time would defeat the allowlist + rate-limit pipeline the
// fire path is supposed to enforce. Fail fast at composition-root time,
// never at first fire.
func NewScheduleFirer(cfg ScheduleFirerConfig) *ScheduleFirer {
	if cfg.Safety == nil {
		panic("app.NewScheduleFirer: Safety is required (FR-003, T1-10 R-03); nil Safety would bypass allowlist + rate limiter") //nolint:forbidigo // T1-10 R-03: fail-fast at composition root; tested by TestNilSafetyPanicsAtWiring
	}
	if cfg.Store == nil {
		panic("app.NewScheduleFirer: Store is required") //nolint:forbidigo // T1-10 R-03: fail-fast at composition root
	}
	if cfg.Sender == nil {
		panic("app.NewScheduleFirer: Sender is required") //nolint:forbidigo // T1-10 R-03: fail-fast at composition root
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &ScheduleFirer{
		store:      cfg.Store,
		drafts:     cfg.Drafts,
		sender:     cfg.Sender,
		safety:     cfg.Safety,
		audit:      cfg.Audit,
		log:        cfg.Log,
		now:        now,
		newID:      cfg.NewID,
		isContact:  cfg.IsContact,
		retryDelay: cmp.Or(cfg.ClaimRetryDelay, time.Second),
	}
}

// draftPayload is the minimal JSON shape persisted in a draft row for
// kind=send / send_media, mirroring the params that the human reviewer
// would replay via draft.approve.
type draftPayload struct {
	To      string `json:"to"`
	Body    string `json:"body,omitempty"`
	Path    string `json:"path,omitempty"`
	Mime    string `json:"mime,omitempty"`
	Caption string `json:"caption,omitempty"`
}

// Fire is the FireFunc handed to ScheduleRunner. Runs at timer expiry.
// Sequential pipeline: load → claim → safety → dispatch (draft gate or send) → upsert.
func (f *ScheduleFirer) Fire(ctx context.Context, profile, id string) {
	log := f.logger()

	var ss domain.ScheduledSend
	if err := f.retry(ctx, profile, id, "load", func() (err error) {
		ss, err = f.store.Get(ctx, profile, id)
		return err
	}); err != nil {
		return
	}
	// Concurrent cancel / duplicate fire short-circuit.
	if ss.State() != domain.SchedulePending {
		return
	}

	// At-most-once claim: persist pending → sending BEFORE any send. If the
	// claim cannot be taken (lost the race to cancel) or stored, nothing is sent.
	claimed, err := ss.MarkSending(f.now())
	if err != nil {
		return
	}
	var won bool
	if err := f.retry(ctx, profile, id, "claim", func() (err error) {
		won, err = f.store.Claim(ctx, claimed)
		return err
	}); err != nil || !won {
		return
	}
	ss = claimed

	// Safety pipeline: allowlist + rate-limiter + warmup, run on the claimed
	// row so a denial is a compare-and-set from sending: a fire holding a
	// stale copy (rescheduled or cancelled meanwhile) loses the claim first and
	// never marks the row failed. A denial is terminal (the window has passed).
	// Safety is guaranteed non-nil by NewScheduleFirer (T1-10 R-03).
	if err := f.safety.Check(ss.Recipient(), domain.ActionSend); err != nil {
		f.markFailed(ctx, ss, err)
		return
	}

	err = f.dispatch(ctx, ss)
	// The writes that settle a claimed row outlive a shutdown: a send that
	// went out must be recorded as such, not left for the next start.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err != nil {
		f.settleError(ctx, ss, err)
		return
	}

	fired, err := ss.MarkFired(f.now())
	if err != nil {
		if !errors.Is(err, domain.ErrScheduleTerminal) {
			log.ErrorContext(ctx, "schedule fire: mark fired",
				slog.String("profile", profile), slog.String("id", id),
				slog.String("err", err.Error()))
		}
		return
	}
	if won, err := f.store.UpdateFrom(ctx, domain.ScheduleSending, fired); err != nil || !won {
		// The send happened. The row stays sending (never pending), so it
		// is not re-sent; the next start marks it send_uncertain.
		log.ErrorContext(ctx, "schedule fire: persist fired failed after send; row left in sending",
			slog.String("profile", profile), slog.String("id", id), slog.Any("err", err), slog.Bool("won", won))
	}
	if f.audit != nil {
		evt := domain.NewAuditEvent("schedule", domain.AuditSend, ss.Recipient(), "ok", id)
		_ = f.audit.Record(ctx, evt)
	}
}

// retry runs op up to claimAttempts times with doubling backoff, counting and
// logging every failure. When it gives up the row is still pending (nothing
// was sent) and is picked up at the next start.
func (f *ScheduleFirer) retry(ctx context.Context, profile, id, what string, op func() error) error {
	var err error
	for i := range claimAttempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(f.retryDelay << (i - 1)):
			}
		}
		if err = op(); err == nil {
			return nil
		}
		if errors.Is(err, fs.ErrNotExist) { // permanent: the row is gone, retrying cannot help
			f.logger().ErrorContext(ctx, "schedule fire: "+what+" failed", slog.String("profile", profile),
				slog.String("id", id), slog.String("err", err.Error()))
			return err
		}
		n := f.claimErrors.Add(1)
		f.logger().WarnContext(ctx, "schedule fire: "+what+" failed, nothing sent",
			slog.String("profile", profile), slog.String("id", id), slog.Int("attempt", i+1),
			slog.Int64("schedule_claim_error_total", n), slog.String("err", err.Error()))
	}
	f.logger().ErrorContext(ctx, "schedule fire: gave up, row stays pending until the next start",
		slog.String("profile", profile), slog.String("id", id), slog.String("step", what))
	return err
}

// dispatch performs the kind-specific action of a due schedule: park it as a
// draft (create_draft, or a send to a contact), or send it directly. Any
// error is mapped to markFailed by the caller.
func (f *ScheduleFirer) dispatch(ctx context.Context, ss domain.ScheduledSend) error {
	switch ss.Kind() {
	case domain.ScheduleKindCreateDraft:
		return f.fireCreateDraft(ctx, ss)
	case domain.ScheduleKindSendText:
		if f.isContact != nil && f.isContact(ss.Recipient()) {
			return f.fireSendAsDraft(ctx, ss, domain.DraftKindSend)
		}
		msg := domain.TextMessage{Recipient: ss.Recipient(), Body: ss.Body()}
		_, err := f.sender.Send(ctx, msg)
		return err
	case domain.ScheduleKindSendMedia:
		if f.isContact != nil && f.isContact(ss.Recipient()) {
			return f.fireSendAsDraft(ctx, ss, domain.DraftKindSendMedia)
		}
		msg := domain.MediaMessage{
			Recipient: ss.Recipient(),
			Path:      ss.MediaPath(),
			Mime:      "application/octet-stream",
			Caption:   ss.Body(),
		}
		_, err := f.sender.Send(ctx, msg)
		return err
	default:
		return fmt.Errorf("unsupported kind %s", ss.Kind())
	}
}

func (f *ScheduleFirer) fireCreateDraft(ctx context.Context, ss domain.ScheduledSend) error {
	return f.createDraft(ctx, ss, domain.DraftKindSend)
}

func (f *ScheduleFirer) fireSendAsDraft(ctx context.Context, ss domain.ScheduledSend, kind domain.DraftKind) error {
	return f.createDraft(ctx, ss, kind)
}

func (f *ScheduleFirer) createDraft(ctx context.Context, ss domain.ScheduledSend, kind domain.DraftKind) error {
	if f.drafts == nil {
		return errors.New("schedule fire: drafts port nil")
	}
	payload := draftPayload{
		To:   ss.Recipient().String(),
		Body: ss.Body(),
	}
	if kind == domain.DraftKindSendMedia {
		payload.Path = ss.MediaPath()
		payload.Mime = "application/octet-stream"
		payload.Caption = ss.Body()
	}
	enc, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal draft payload: %w", err)
	}
	id := ss.ID()
	if f.newID != nil {
		id = f.newID()
	}
	draft, err := domain.NewDraft(id, ss.Profile(), kind, string(enc), ss.Recipient(), f.now().Unix())
	if err != nil {
		return fmt.Errorf("build draft: %w", err)
	}
	if err := f.drafts.Put(ctx, draft); err != nil {
		return fmt.Errorf("persist draft: %w", err)
	}
	return nil
}

// settleError records a dispatch error on a claimed row. Only an error proven
// to precede the wire (denied, rate limited, not connected, validation, a
// draft that could not be stored) is a terminal failure. A typed
// ErrSendUncertain, or a context ending mid-send that cannot be placed
// before the write, may have been delivered: the row becomes send_uncertain
// (never retried, never "failed") so a reschedule cannot duplicate it.
func (f *ScheduleFirer) settleError(ctx context.Context, ss domain.ScheduledSend, cause error) {
	if !errors.Is(cause, domain.ErrSendUncertain) && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		f.markFailed(ctx, ss, cause)
		return
	}
	reason := "send outcome unknown, not retried: " + cause.Error()
	if u := new(domain.SendUncertainError); errors.As(cause, &u) && u.MessageID != "" {
		reason += " (message id " + string(u.MessageID) + ")"
	}
	uncertain, err := ss.MarkSendUncertain(f.now(), reason)
	if err != nil {
		return
	}
	f.logger().WarnContext(ctx, "schedule fire: send outcome uncertain, not retried",
		slog.String("profile", ss.Profile()), slog.String("id", ss.ID()), slog.String("err", cause.Error()))
	f.settle(ctx, ss, uncertain, "uncertain", reason)
}

func (f *ScheduleFirer) markFailed(ctx context.Context, ss domain.ScheduledSend, cause error) {
	failed, err := ss.MarkFailed(f.now(), cause.Error())
	if err != nil {
		return
	}
	f.settle(ctx, ss, failed, "error", cause.Error())
}

// settle persists next as a compare-and-set from the state the caller holds
// (pending for a denial before the claim, sending after): a duplicate fire
// that lost the race must not overwrite a row another fire already settled,
// nor audit it.
func (f *ScheduleFirer) settle(ctx context.Context, ss, next domain.ScheduledSend, decision, detail string) {
	won, err := f.store.UpdateFrom(ctx, ss.State(), next)
	if err != nil {
		f.logger().ErrorContext(ctx, "schedule fire: persist "+decision+" failed",
			slog.String("profile", ss.Profile()), slog.String("id", ss.ID()),
			slog.String("err", err.Error()))
	}
	if err == nil && !won {
		return
	}
	if f.audit != nil {
		_ = f.audit.Record(ctx, domain.NewAuditEvent("schedule", domain.AuditSend, ss.Recipient(), decision, detail))
	}
}

func (f *ScheduleFirer) logger() *slog.Logger {
	if f.log != nil {
		return f.log
	}
	return slog.Default()
}
