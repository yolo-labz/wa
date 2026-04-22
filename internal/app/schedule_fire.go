package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// ScheduleFirer wires a ScheduledSend's FireFunc callback to the full
// outbound safety pipeline (FR-003, FR-112): re-load the row, run allowlist
// + rate-limit + warmup checks, then dispatch by kind — direct send for
// send_text/send_media, or a draft row for create_draft. Terminal state
// transitions (fired / failed) are persisted via ScheduledStore.Update so
// a subsequent daemon restart does not retry the row.
//
// Idempotency at fire time is provided by the store-level pending-guard:
// MarkFired is a one-way transition and ErrScheduleTerminal short-circuits
// any duplicate fire that wins the race after the timer callback has
// already run once.
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
}

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
		store:     cfg.Store,
		drafts:    cfg.Drafts,
		sender:    cfg.Sender,
		safety:    cfg.Safety,
		audit:     cfg.Audit,
		log:       cfg.Log,
		now:       now,
		newID:     cfg.NewID,
		isContact: cfg.IsContact,
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
// Sequential pipeline: load → safety → draft gate → send → upsert.
// Branch count tracks pipeline stages; extracting them would scatter
// audit-facing error mapping.
//
//nolint:gocyclo // one branch per pipeline stage
func (f *ScheduleFirer) Fire(ctx context.Context, profile, id string) {
	log := f.logger()

	ss, err := f.store.Get(ctx, profile, id)
	if err != nil {
		log.ErrorContext(ctx, "schedule fire: load failed",
			slog.String("profile", profile), slog.String("id", id),
			slog.String("err", err.Error()))
		return
	}
	// Concurrent cancel / duplicate fire short-circuit.
	if ss.State() != domain.SchedulePending {
		return
	}

	// Safety pipeline: allowlist + rate-limiter + warmup. A denial is
	// treated as terminal failure (the scheduled fire-window has passed).
	// Safety is guaranteed non-nil by NewScheduleFirer (T1-10 R-03).
	if err := f.safety.Check(ss.Recipient(), domain.ActionSend); err != nil {
		f.markFailed(ctx, ss, err)
		return
	}

	switch ss.Kind() {
	case domain.ScheduleKindCreateDraft:
		if err := f.fireCreateDraft(ctx, ss); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	case domain.ScheduleKindSendText:
		if f.isContact != nil && f.isContact(ss.Recipient()) {
			if err := f.fireSendAsDraft(ctx, ss, domain.DraftKindSend); err != nil {
				f.markFailed(ctx, ss, err)
				return
			}
			break
		}
		msg := domain.TextMessage{Recipient: ss.Recipient(), Body: ss.Body()}
		if _, err := f.sender.Send(ctx, msg); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	case domain.ScheduleKindSendMedia:
		if f.isContact != nil && f.isContact(ss.Recipient()) {
			if err := f.fireSendAsDraft(ctx, ss, domain.DraftKindSendMedia); err != nil {
				f.markFailed(ctx, ss, err)
				return
			}
			break
		}
		msg := domain.MediaMessage{
			Recipient: ss.Recipient(),
			Path:      ss.MediaPath(),
			Mime:      "application/octet-stream",
			Caption:   ss.Body(),
		}
		if _, err := f.sender.Send(ctx, msg); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	default:
		f.markFailed(ctx, ss, fmt.Errorf("unsupported kind %s", ss.Kind()))
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
	if err := f.store.Update(ctx, fired); err != nil {
		log.ErrorContext(ctx, "schedule fire: persist fired",
			slog.String("profile", profile), slog.String("id", id),
			slog.String("err", err.Error()))
	}
	if f.audit != nil {
		evt := domain.NewAuditEvent("schedule", domain.AuditSend, ss.Recipient(), "ok", id)
		_ = f.audit.Record(ctx, evt)
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
		return fmt.Errorf("schedule fire: drafts port nil")
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

func (f *ScheduleFirer) markFailed(ctx context.Context, ss domain.ScheduledSend, cause error) {
	failed, err := ss.MarkFailed(f.now(), cause.Error())
	if err != nil {
		return
	}
	if err := f.store.Update(ctx, failed); err != nil {
		f.logger().ErrorContext(ctx, "schedule fire: persist failed",
			slog.String("profile", ss.Profile()), slog.String("id", ss.ID()),
			slog.String("err", err.Error()))
	}
	if f.audit != nil {
		evt := domain.NewAuditEvent("schedule", domain.AuditSend, ss.Recipient(), "error", cause.Error())
		_ = f.audit.Record(ctx, evt)
	}
}

func (f *ScheduleFirer) logger() *slog.Logger {
	if f.log != nil {
		return f.log
	}
	return slog.Default()
}
