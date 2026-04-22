package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	waEvents "go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/domain"
)

// BlockerAdapter is the whatsmeow-backed implementation of app.Blocker
// (FR-018, FR-019). Block / Unblock go out over UpdateBlocklist and write
// AuditBlock / AuditUnblock on success; ListBlocked reads GetBlocklist
// live from the server per the "no local cache" rule in ports_018.go so
// out-of-band unblocks from the phone UI are reflected immediately.
//
// The adapter records audit entries only on a successful upstream call.
// A transport failure propagates unchanged without an audit side effect —
// per CLAUDE.md rule 12 ("no silent fallbacks") callers see the raw error
// and can retry.
type BlockerAdapter struct {
	client whatsmeowClient
	audit  *auditRingBuffer
	logger *slog.Logger
	nowFn  func() time.Time
}

// NewBlockerFor wires a BlockerAdapter to the Adapter's whatsmeow client,
// audit ring, logger, and clock. Returns an error only if the Adapter or
// its client is nil — the composition root surfaces that as a startup
// failure rather than a nil-pointer panic later.
func (a *Adapter) NewBlockerFor() (*BlockerAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewBlockerFor: nil adapter/client")
	}
	return &BlockerAdapter{
		client: a.client,
		audit:  a.auditBuf,
		logger: a.logger,
		nowFn:  a.nowFn,
	}, nil
}

// Block implements app.Blocker. Idempotent on a server-side already-blocked
// JID: UpdateBlocklist returns nil and the audit is still recorded so
// forensic tooling sees every request.
func (b *BlockerAdapter) Block(ctx context.Context, jid domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jid.IsZero() {
		return fmt.Errorf("BlockerAdapter.Block: %w", domain.ErrInvalidJID)
	}
	if !b.client.IsConnected() {
		return fmt.Errorf("BlockerAdapter.Block: %w", domain.ErrDisconnected)
	}
	if _, err := b.client.UpdateBlocklist(ctx, toWhatsmeow(jid), waEvents.BlocklistChangeActionBlock); err != nil {
		return fmt.Errorf("BlockerAdapter.Block: %w", err)
	}
	b.recordAudit(domain.AuditBlock, jid, "ok", "")
	return nil
}

// Unblock implements app.Blocker. Idempotent on a never-blocked JID per
// BL4 — UpdateBlocklist tolerates the absent case server-side.
func (b *BlockerAdapter) Unblock(ctx context.Context, jid domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jid.IsZero() {
		return fmt.Errorf("BlockerAdapter.Unblock: %w", domain.ErrInvalidJID)
	}
	if !b.client.IsConnected() {
		return fmt.Errorf("BlockerAdapter.Unblock: %w", domain.ErrDisconnected)
	}
	if _, err := b.client.UpdateBlocklist(ctx, toWhatsmeow(jid), waEvents.BlocklistChangeActionUnblock); err != nil {
		return fmt.Errorf("BlockerAdapter.Unblock: %w", err)
	}
	b.recordAudit(domain.AuditUnblock, jid, "ok", "")
	return nil
}

// ListBlocked implements app.Blocker. Reads GetBlocklist live from the
// server — no local cache — so out-of-band unblocks (phone UI) reflect
// immediately, per ports_018.go.
func (b *BlockerAdapter) ListBlocked(ctx context.Context) ([]domain.JID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !b.client.IsConnected() {
		return nil, fmt.Errorf("BlockerAdapter.ListBlocked: %w", domain.ErrDisconnected)
	}
	list, err := b.client.GetBlocklist(ctx)
	if err != nil {
		return nil, fmt.Errorf("BlockerAdapter.ListBlocked: %w", err)
	}
	if list == nil {
		return nil, nil
	}
	out := make([]domain.JID, 0, len(list.JIDs))
	for _, j := range list.JIDs {
		dj, convErr := toDomain(j)
		if convErr != nil {
			// Skip malformed JIDs rather than failing the whole list —
			// the server occasionally returns legacy-format entries.
			if b.logger != nil {
				b.logger.Warn("BlockerAdapter.ListBlocked: skipping malformed JID", "raw", j.String(), "err", convErr)
			}
			continue
		}
		out = append(out, dj)
	}
	return out, nil
}

func (b *BlockerAdapter) recordAudit(action domain.AuditAction, subject domain.JID, decision, detail string) {
	if b.audit == nil {
		return
	}
	_ = b.audit.Record(context.Background(), domain.AuditEvent{
		TS:       b.nowFn(),
		Actor:    "whatsmeow",
		Action:   action,
		Subject:  subject,
		Decision: decision,
		Detail:   detail,
	})
}
