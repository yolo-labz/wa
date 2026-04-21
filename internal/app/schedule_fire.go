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
// outbound safety pipeline (FR-112): re-load the row, run allowlist +
// rate-limit + warmup checks, then dispatch by kind — direct send for
// send_text/send_media, or a draft row for create_draft. Terminal state
// transitions (fired / failed) are persisted via ScheduledStore.Update so
// a subsequent daemon restart does not retry the row.
//
// Idempotency at fire time is provided by the store-level pending-guard:
// MarkFired is a one-way transition and ErrScheduleTerminal short-circuits
// any duplicate fire that wins the race after the timer callback has
// already run once.
type ScheduleFirer struct {
	Store     ScheduledStore
	Drafts    DraftStore
	Sender    MessageSender
	Safety    *SafetyPipeline
	Audit     AuditLog
	Log       *slog.Logger
	Now       func() time.Time
	NewID     func() string         // draft-id generator; defaults to ULID
	IsContact func(domain.JID) bool // optional: when true, send_text is routed to a draft
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
//
//nolint:gocyclo // sequential pipeline: load → rate-limit → allowlist
// → draft gate → idempotency → send → upsert; branches track pipeline
// stages, extracting them would scatter the audit-facing error mapping.
func (f *ScheduleFirer) Fire(ctx context.Context, profile, id string) {
	log := f.logger()

	ss, err := f.Store.Get(ctx, profile, id)
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
	if f.Safety != nil {
		if err := f.Safety.Check(ss.Recipient(), domain.ActionSend); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	}

	switch ss.Kind() {
	case domain.ScheduleKindCreateDraft:
		if err := f.fireCreateDraft(ctx, ss); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	case domain.ScheduleKindSendText:
		if f.IsContact != nil && f.IsContact(ss.Recipient()) {
			if err := f.fireSendAsDraft(ctx, ss, domain.DraftKindSend); err != nil {
				f.markFailed(ctx, ss, err)
				return
			}
			break
		}
		msg := domain.TextMessage{Recipient: ss.Recipient(), Body: ss.Body()}
		if _, err := f.Sender.Send(ctx, msg); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	case domain.ScheduleKindSendMedia:
		if f.IsContact != nil && f.IsContact(ss.Recipient()) {
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
		if _, err := f.Sender.Send(ctx, msg); err != nil {
			f.markFailed(ctx, ss, err)
			return
		}
	default:
		f.markFailed(ctx, ss, fmt.Errorf("unsupported kind %s", ss.Kind()))
		return
	}

	fired, err := ss.MarkFired(f.Now())
	if err != nil {
		if !errors.Is(err, domain.ErrScheduleTerminal) {
			log.ErrorContext(ctx, "schedule fire: mark fired",
				slog.String("profile", profile), slog.String("id", id),
				slog.String("err", err.Error()))
		}
		return
	}
	if err := f.Store.Update(ctx, fired); err != nil {
		log.ErrorContext(ctx, "schedule fire: persist fired",
			slog.String("profile", profile), slog.String("id", id),
			slog.String("err", err.Error()))
	}
	if f.Audit != nil {
		evt := domain.NewAuditEvent("schedule", domain.AuditSend, ss.Recipient(), "ok", id)
		_ = f.Audit.Record(ctx, evt)
	}
}

func (f *ScheduleFirer) fireCreateDraft(ctx context.Context, ss domain.ScheduledSend) error {
	return f.createDraft(ctx, ss, domain.DraftKindSend)
}

func (f *ScheduleFirer) fireSendAsDraft(ctx context.Context, ss domain.ScheduledSend, kind domain.DraftKind) error {
	return f.createDraft(ctx, ss, kind)
}

func (f *ScheduleFirer) createDraft(ctx context.Context, ss domain.ScheduledSend, kind domain.DraftKind) error {
	if f.Drafts == nil {
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
	if f.NewID != nil {
		id = f.NewID()
	}
	draft, err := domain.NewDraft(id, ss.Profile(), kind, string(enc), ss.Recipient(), f.Now().Unix())
	if err != nil {
		return fmt.Errorf("build draft: %w", err)
	}
	if err := f.Drafts.Put(ctx, draft); err != nil {
		return fmt.Errorf("persist draft: %w", err)
	}
	return nil
}

func (f *ScheduleFirer) markFailed(ctx context.Context, ss domain.ScheduledSend, cause error) {
	failed, err := ss.MarkFailed(f.Now(), cause.Error())
	if err != nil {
		return
	}
	if err := f.Store.Update(ctx, failed); err != nil {
		f.logger().ErrorContext(ctx, "schedule fire: persist failed",
			slog.String("profile", ss.Profile()), slog.String("id", ss.ID()),
			slog.String("err", err.Error()))
	}
	if f.Audit != nil {
		evt := domain.NewAuditEvent("schedule", domain.AuditSend, ss.Recipient(), "error", cause.Error())
		_ = f.Audit.Record(ctx, evt)
	}
}

func (f *ScheduleFirer) logger() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}
