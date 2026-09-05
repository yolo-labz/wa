package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	waTypes "go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// deleteMessageForMeVersion is the app-state mutation version for the
// deleteMessageForMe index. whatsmeow ships no BuildDeleteMessageForMe, so
// this value cannot be taken from the library; it is confirmed against
// Baileys src/Utils/chat-utils.ts (`apiVersion: 3` for the same index in the
// same regular_high collection). See specs/115-delete-for-me/spec.md.
const deleteMessageForMeVersion int32 = 3

// moderatorHistory is the narrow secondary-side interface consulted by the
// Moderator. It is a subset of sqlitehistory.Store deliberately separated
// from the broader historyContainer so that existing test doubles for
// markread / history_sync do not need moderation methods they do not use
// (CLAUDE.md rule 8 — narrow interface at the consumer).
type moderatorHistory interface {
	GetMessageMeta(ctx context.Context, chat domain.JID, id domain.MessageID) (ts int64, body string, err error)
	GetMessageIdentity(ctx context.Context, chat domain.JID, id domain.MessageID) (sender string, fromMe bool, ts int64, err error)
	StampRevoke(ctx context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope, revokedAt int64) error
	StampEdit(ctx context.Context, chat domain.JID, id domain.MessageID, newBody string, editedAt int64) error
}

// Moderator is the whatsmeow-backed implementation of app.MessageModerator
// (FR-014, FR-015, FR-015a). Two responsibilities in one struct:
//
//   - Revoke(scope=self)      → deleteMessageForMe app-state patch + local stamp.
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

	// Both scopes hit the network before stamping, and for the same reason:
	// a failed call must NOT leave a row the caller believes dead. They use
	// unrelated mechanisms, though — everyone is an encrypted ProtocolMessage
	// peers act on, self is an app-state mutation only our own devices ever
	// see. Feature 115 wired the second one; before it, scope=self stamped
	// the local row and returned success having deleted nothing.
	if !m.client.IsConnected() {
		return fmt.Errorf("Moderator.Revoke: %w", domain.ErrDisconnected)
	}
	switch scope {
	case domain.RevokeEveryone:
		tombstone := m.client.BuildRevoke(waChat, waTypes.EmptyJID, waTypes.MessageID(id))
		if _, err := m.client.SendMessage(ctx, waChat, tombstone); err != nil {
			m.recordAudit(domain.AuditRevoke, chat, "error", err.Error())
			return fmt.Errorf("Moderator.Revoke: %w", err)
		}
	case domain.RevokeSelf:
		patch, err := m.buildDeleteForMe(ctx, chat, waChat, id)
		if err != nil {
			m.recordAudit(domain.AuditRevoke, chat, "error", err.Error())
			return err
		}
		if err := m.client.SendAppState(ctx, patch); err != nil {
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

// buildDeleteForMe assembles the deleteMessageForMe app-state patch for one
// message (FR-115-1..3, FR-115-6). whatsmeow exports the index constant, the
// action protobuf and PatchInfo, but no builder — so the mutation is composed
// here, mirroring appstate.BuildStar, which is the same regular_high
// collection and the same five-part per-message index.
//
// The index is an addressing key: a wrong one is silently accepted and
// deletes nothing, so every part is read from the stored row rather than
// guessed. A missing row is therefore a hard error (FR-115-7), not a
// best-effort patch.
func (m *Moderator) buildDeleteForMe(ctx context.Context, chat domain.JID, waChat waTypes.JID, id domain.MessageID) (appstate.PatchInfo, error) {
	sender, fromMe, ts, err := m.history.GetMessageIdentity(ctx, chat, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return appstate.PatchInfo{}, fmt.Errorf("Moderator.Revoke: %s/%s: %w", chat, id, domain.ErrMessageUnknown)
		}
		return appstate.PatchInfo{}, fmt.Errorf("Moderator.Revoke: %w", err)
	}

	isFromMe := "0"
	if fromMe {
		isFromMe = "1"
	}
	// Participant slot: the third-party sender for a group message, "0" when
	// the message is not from a third party. Same collapse rule as
	// appstate.BuildStar, which compares JID users rather than strings so a
	// device suffix does not defeat the match.
	participant := "0"
	if senderJID, parseErr := waTypes.ParseJID(sender); parseErr == nil && senderJID.User != waChat.User {
		participant = senderJID.String()
	}

	return appstate.PatchInfo{
		Type: appstate.WAPatchRegularHigh,
		Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexDeleteMessageForMe, waChat.String(), string(id), isFromMe, participant},
			Version: deleteMessageForMeVersion,
			Value: &waSyncAction.SyncActionValue{
				DeleteMessageForMeAction: &waSyncAction.DeleteMessageForMeAction{
					// media.gc owns files on disk. Coupling the two would make
					// one irreversible call perform two irreversible actions.
					DeleteMedia:      proto.Bool(false),
					MessageTimestamp: proto.Int64(time.Unix(ts, 0).UnixMilli()),
				},
			},
		}},
	}, nil
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
