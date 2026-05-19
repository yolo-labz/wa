package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// LogoutAllAdapter is the whatsmeow-backed implementation of
// app.SessionTerminator (FR-031). It unlinks every device from the
// WhatsApp account, not just the local client — distinct from
// Client.Logout which only clears the local session.
//
// As of the whatsmeow commit pinned by feature 003 commit 1, the upstream
// library exposes only Client.Logout(ctx) — there is NO Client.LogoutAll
// helper. Per T2-12 the adapter routes that absence to -32000
// upstream_error (domain.ErrUpstreamError) with a caller-visible message.
// When whatsmeow gains a LogoutAll helper, this adapter will call it and
// write AuditLogoutAll on success; the test TestLogoutAllUnsupportedSurfaced
// flips to TestLogoutAllWritesAudit at that point.
//
// Tracking: https://github.com/tulir/whatsmeow/issues (search LogoutAll).
type LogoutAllAdapter struct {
	client whatsmeowClient
	audit  *auditRingBuffer
	logger *slog.Logger
	nowFn  func() time.Time
}

// NewLogoutAllFor wires a LogoutAllAdapter to the Adapter's whatsmeow
// client, audit ring, logger, and clock.
func (a *Adapter) NewLogoutAllFor() (*LogoutAllAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewLogoutAllFor: nil adapter/client")
	}
	return &LogoutAllAdapter{
		client: a.client,
		audit:  a.auditBuf,
		logger: a.logger,
		nowFn:  a.nowFn,
	}, nil
}

// LogoutAll implements app.SessionTerminator. Returns domain.ErrUpstreamError
// unconditionally at the current whatsmeow commit pin — the upstream does
// not expose a LogoutAll helper. No audit is written on failure, consistent
// with every other port in this package.
func (l *LogoutAllAdapter) LogoutAll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Upstream helper absent at the pinned whatsmeow commit. Surface as
	// -32000 upstream_error; callers (CLI, plugin shim) display the
	// full message so the user understands the account is unchanged.
	return fmt.Errorf("LogoutAllAdapter.LogoutAll: %w: whatsmeow Client.LogoutAll not present in pinned commit", domain.ErrUpstreamError)
}

// Logout implements the second SessionTerminator method (feature 110f).
// Wraps whatsmeow Client.Logout(ctx) which performs:
//  1. Server-side IQ to WhatsApp asking to unlink this device.
//  2. cli.Disconnect() — close the websocket.
//  3. cli.Store.Delete(ctx) — delete the whatsmeow_device row + related
//     identity/sender keys from the session SQLite (NOT messages.db).
//
// After this returns, the daemon's *whatsmeow.Client has no device ID; the
// next pair RPC will see Store.ID == nil and proceed with a fresh QR
// (or phone-code) handshake. messages.db is a separate SQLite file
// (sqlitehistory package) and is untouched.
//
// Writes an AuditLogout entry on success. Failure does not emit audit
// (success-only record per CLAUDE.md rule 12). Returns a wrapped
// "not paired" error from whatsmeow if the daemon has no current device.
func (l *LogoutAllAdapter) Logout(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store := l.client.Store()
	if store == nil || store.ID == nil {
		return errors.New("LogoutAllAdapter.Logout: not paired (no current device)")
	}
	if err := l.client.Logout(ctx); err != nil {
		return fmt.Errorf("LogoutAllAdapter.Logout: %w", err)
	}
	if l.audit != nil {
		_ = l.audit.Record(ctx, domain.AuditEvent{
			TS:     l.nowFn(),
			Action: domain.AuditLogout,
		})
	}
	if l.logger != nil {
		l.logger.Info("whatsmeow Logout completed — device unlinked, session cleared, messages.db preserved")
	}
	return nil
}
