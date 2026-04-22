package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
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
