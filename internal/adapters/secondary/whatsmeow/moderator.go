package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// moderatorHistory is the narrow secondary-side interface consulted by the
// Moderator. It is a subset of sqlitehistory.Store deliberately separated
// from the broader historyContainer so that existing test doubles for
// markread / history_sync do not need moderation methods they do not use
// (CLAUDE.md rule 8 — narrow interface at the consumer).
type moderatorHistory interface {
	GetMessageMeta(ctx context.Context, chat domain.JID, id domain.MessageID) (ts int64, body string, err error)
	StampRevoke(ctx context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope, revokedAt int64) error
	StampEdit(ctx context.Context, chat domain.JID, id domain.MessageID, newBody string, editedAt int64) error
}

// Moderator is the whatsmeow-backed implementation of app.MessageModerator
// (FR-014, FR-015, FR-015a). Two responsibilities in one struct:
//
//   - Revoke(scope=self)      → local persistence only (no network).
//   - Revoke(scope=everyone)  → ProtocolMessage REVOKE tombstone + local stamp.
//   - Edit(ctx, chat, id, b)  → window-gated ProtocolMessage MESSAGE_EDIT +
//     previous_body preservation.
//
// Kept as a separate struct rather than a method on Adapter: the 016 audit
// flagged Adapter at 22 fields; new ports get their own focused structs.
// Composition is shared state (client, history, nowFn, audit) passed by
// value pointer from the composition root — no hidden back-references.
type Moderator struct {
	client  whatsmeowClient
	history moderatorHistory
	audit   *auditRingBuffer
	logger  *slog.Logger
	nowFn   func() time.Time
}

// NewModerator constructs a Moderator. client and history are required;
// a nil value returns an error rather than panicking so the composition
// root surfaces misconfiguration loudly. nowFn may be nil, in which case
// time.Now is used.
func NewModerator(client whatsmeowClient, history moderatorHistory, audit *auditRingBuffer, logger *slog.Logger, nowFn func() time.Time) (*Moderator, error) {
	if client == nil {
		return nil, errors.New("whatsmeow.NewModerator: nil client")
	}
	if history == nil {
		return nil, errors.New("whatsmeow.NewModerator: nil history")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Moderator{
		client:  client,
		history: history,
		audit:   audit,
		logger:  logger,
		nowFn:   nowFn,
	}, nil
}

// NewModeratorFor is the Adapter-method factory used by the composition
// root. It binds the supplied history store (the sqlitehistory.Store from
// cmd/wad) to the Adapter's client, audit ring, logger, and clock.
func (a *Adapter) NewModeratorFor(history moderatorHistory) (*Moderator, error) {
	return NewModerator(a.client, history, a.auditBuf, a.logger, a.nowFn)
}

// Revoke implements app.MessageModerator. FR-014.
func (m *Moderator) Revoke(ctx context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("Moderator.Revoke: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("Moderator.Revoke: empty message id")
	}
	if !scope.IsValid() {
		return fmt.Errorf("Moderator.Revoke: invalid scope %d", uint8(scope))
	}

	now := m.nowFn()
	waChat := toWhatsmeow(chat)

	// scope=everyone sends the ProtocolMessage REVOKE tombstone first; a
	// network failure must NOT stamp the local row (peers would otherwise
	// see a message we consider dead). scope=self skips the network and
	// only touches the local DB.
	if scope == domain.RevokeEveryone {
		if !m.client.IsConnected() {
			return fmt.Errorf("Moderator.Revoke: %w", domain.ErrDisconnected)
		}
		tombstone := m.client.BuildRevoke(waChat, waTypes.EmptyJID, waTypes.MessageID(id))
		if _, err := m.client.SendMessage(ctx, waChat, tombstone); err != nil {
			m.recordAudit(domain.AuditRevoke, chat, "error", err.Error())
			return fmt.Errorf("Moderator.Revoke: %w", err)
		}
	}

	if err := m.history.StampRevoke(ctx, chat, id, scope, now.Unix()); err != nil {
		m.recordAudit(domain.AuditRevoke, chat, "error", err.Error())
		return fmt.Errorf("Moderator.Revoke: %w", err)
	}

	m.recordAudit(domain.AuditRevoke, chat, "ok", string(id)+":"+scope.String())
	return nil
}

// Edit implements app.MessageModerator. FR-015 + FR-015a.
func (m *Moderator) Edit(ctx context.Context, chat domain.JID, id domain.MessageID, newBody string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("Moderator.Edit: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("Moderator.Edit: empty message id")
	}
	if newBody == "" {
		return errors.New("Moderator.Edit: empty newBody")
	}
	if len(newBody) > domain.MaxTextBytes {
		return fmt.Errorf("%w: edit body %d > %d bytes", domain.ErrMessageTooLarge, len(newBody), domain.MaxTextBytes)
	}

	ts, _, err := m.history.GetMessageMeta(ctx, chat, id)
	if err != nil {
		return fmt.Errorf("Moderator.Edit: %w", err)
	}

	now := m.nowFn()
	if now.Sub(time.Unix(ts, 0)) > domain.EditWindow {
		return fmt.Errorf("%w: original ts=%d now=%d", domain.ErrOutsideEditWindow, ts, now.Unix())
	}

	if !m.client.IsConnected() {
		return fmt.Errorf("Moderator.Edit: %w", domain.ErrDisconnected)
	}

	waChat := toWhatsmeow(chat)
	body := newBody
	newContent := &waE2E.Message{Conversation: &body}
	editMsg := m.client.BuildEdit(waChat, waTypes.MessageID(id), newContent)
	if _, err := m.client.SendMessage(ctx, waChat, editMsg); err != nil {
		m.recordAudit(domain.AuditEdit, chat, "error", err.Error())
		return fmt.Errorf("Moderator.Edit: %w", err)
	}

	if err := m.history.StampEdit(ctx, chat, id, newBody, now.Unix()); err != nil {
		m.recordAudit(domain.AuditEdit, chat, "error", err.Error())
		return fmt.Errorf("Moderator.Edit: %w", err)
	}

	m.recordAudit(domain.AuditEdit, chat, "ok", string(id))
	return nil
}

func (m *Moderator) recordAudit(action domain.AuditAction, subject domain.JID, decision, detail string) {
	if m.audit == nil {
		return
	}
	_ = m.audit.Record(context.Background(), domain.AuditEvent{
		TS:       m.nowFn(),
		Actor:    "whatsmeow",
		Action:   action,
		Subject:  subject,
		Decision: decision,
		Detail:   detail,
	})
}
