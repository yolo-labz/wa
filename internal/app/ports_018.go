package app

import (
	"context"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// This file declares the secondary ports introduced by feature 018
// (parity-hardening) Tier 2. They are kept in a dedicated file for
// reviewability; the composition root wires concrete adapters to these
// interfaces in the same way as the pre-018 ports.
//
// Port-hygiene rationale per constitution IV.21: each interface describes
// an "intent of conversation" (CLAUDE.md rule 21) rather than a piece of
// whatsmeow surface. Every mutating method of every port below is
// allowlist-gated at the socket dispatcher and idempotency-wrapped via
// IdempotencyStore.LoadOrStore — neither concern leaks into these
// signatures, keeping port surfaces simpler than their implementations
// (Ousterhout deep-module ratio, CLAUDE.md rule 8).
//
// Every port in this file is additive: no existing signature changes.
// New MessageSender capabilities are introduced as extension interfaces
// (ForwardSender, StarSender) that use cases type-assert on, matching the
// 017 ReplySender / PresenceSender pattern.

// MessageModerator is the FR-014/FR-015 port for moderating already-sent
// messages. Revoke removes a message; Edit rewrites its body.
//
// WhatsApp implements the two scopes with two unrelated mechanisms, and
// the distinction is the whole contract here:
//   - Revoke with RevokeSelf pushes a deleteMessageForMe app-state
//     mutation (regular_high collection) so the caller's OWN devices drop
//     the message. It MUST NOT emit a ProtocolMessage tombstone — no peer
//     ever learns of a delete-for-me. Until feature 115 this branch only
//     stamped the local row, which reported success while deleting
//     nothing.
//   - Revoke with RevokeEveryone sends a ProtocolMessage REVOKE so peers
//     delete their copies.
//   - Either scope stamps local persistence only AFTER its network call
//     succeeds; a failed call must not leave a row the caller believes
//     dead.
//   - Neither scope is reversible. Nothing in the protocol undoes either.
//
// Implementations MUST also:
//   - Edit refuses newBody outside the 15-minute window from the original
//     timestamp with a typed error that the socket dispatcher maps to
//     -32100 policy_refused (FR-015a).
//   - Edit persists the original body in previous_body and stamps
//     edited_at on the original row (schema v3→v4 migration).
//   - Be safe for concurrent use by multiple goroutines.
//   - Honour ctx cancellation.
type MessageModerator interface {
	Revoke(ctx context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope) error
	Edit(ctx context.Context, chat domain.JID, id domain.MessageID, newBody string) error
}

// ChatStateManager is the FR-016 port for chat-level UI state: archive,
// mute, pin, mark-unread.
//
// Implementations MUST:
//   - Archive is a toggle; archiving an already-archived chat is a no-op
//     that returns nil.
//   - Mute accepts nil (unmute) OR a future timestamp. A past timestamp
//     returns a typed error routed to -32100 policy_refused.
//   - Pin is a toggle; whatsmeow's upstream 3-pin cap surfaces as
//     -32000 upstream_error from the adapter.
//   - MarkUnread is idempotent; re-marking an already-unread chat returns
//     nil.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type ChatStateManager interface {
	Archive(ctx context.Context, chat domain.JID, archived bool) error
	Mute(ctx context.Context, chat domain.JID, until *time.Time) error
	Pin(ctx context.Context, chat domain.JID, pinned bool) error
	MarkUnread(ctx context.Context, chat domain.JID) error
}

// Blocker is the FR-018/FR-019 port for contact blocking.
//
// Implementations MUST:
//   - Write an AuditBlock / AuditUnblock entry on every successful call.
//   - Surface an attempt to send to a blocked JID as -32100 policy_refused
//     (the dispatcher enforces this by consulting ListBlocked BEFORE the
//     send — Blocker does NOT gate MessageSender directly).
//   - ListBlocked reads live from the server, NOT a local cache, so
//     out-of-band unblocks (phone UI) are reflected immediately.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type Blocker interface {
	Block(ctx context.Context, jid domain.JID) error
	Unblock(ctx context.Context, jid domain.JID) error
	ListBlocked(ctx context.Context) ([]domain.JID, error)
}

// PrivacySettings is the FR-029/FR-030 port for account-level privacy
// dimensions (groups, readReceipts, lastSeen, profile, about).
//
// Implementations MUST:
//   - Validate the (key, value) tuple against domain.PrivacyTuple.Validate
//     before any I/O.
//   - Read from the server on Get (no local cache) so the returned
//     settings reflect changes made from other devices.
//   - Write an AuditPrivacyChange entry on every successful Set.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type PrivacySettings interface {
	Set(ctx context.Context, tuple domain.PrivacyTuple) error
	Get(ctx context.Context) ([]domain.PrivacyTuple, error)
}

// ProfileEditor is the FR-027/FR-028 port for account identity edits.
//
// Implementations MUST:
//   - SetName rejects name > 25 chars with a typed error routed to
//     -32100 policy_refused.
//   - SetStatus rejects status > 139 chars with a typed error routed to
//     -32100 policy_refused.
//   - GetAvatar returns a filesystem path under
//     `$XDG_CACHE_HOME/wa/media/avatars/sha256/<2>/<rest>.jpg` where the
//     content is already fetched and verified — repeated calls for the
//     same JID+size return the cached path without network I/O until the
//     server-side hash changes.
//   - Write an AuditProfileEdit entry on every successful SetName /
//     SetStatus call (GetAvatar is read-only, no audit).
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type ProfileEditor interface {
	SetName(ctx context.Context, name string) error
	SetStatus(ctx context.Context, status string) error
	GetAvatar(ctx context.Context, jid domain.JID, size domain.AvatarSize) (path string, err error)
}

// GroupAdmin is the FR-020..FR-025 port for group lifecycle. Its ten
// methods split into three clusters: creation/exit (Create, Leave),
// roster management (AddParticipants, RemoveParticipants, Promote,
// Demote), and metadata (Edit, InviteGet, InviteRevoke, InviteJoin).
//
// Implementations MUST:
//   - Create respects the ≤ 5 groups / day rate-limiter at the adapter
//     boundary; excess calls return -32200 rate_limited.
//   - AddParticipants respects the ≤ 50 adds / day rate-limiter; excess
//     calls return -32200 rate_limited.
//   - Admin-only methods (Promote, Demote, AddParticipants,
//     RemoveParticipants, Edit) surface upstream "not-admin" as
//     -32100 policy_refused.
//   - Edit refuses a GroupPatch where every field is nil — the
//     domain.GroupPatch.Validate check is the single authority.
//   - InviteJoin URL MUST match `^https://chat\.whatsapp\.com/[A-Za-z0-9]+$`;
//     upstream "expired link" errors surface as -32000 upstream_error.
//   - Write the appropriate AuditKind entry on every successful mutating
//     call (AuditGroupCreate, AuditLeaveGroup,
//     AuditGroupParticipantsPatch, AuditGroupInviteJoin). InviteGet is
//     read-only, no audit.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type GroupAdmin interface {
	Create(ctx context.Context, subject string, participants []domain.JID) (domain.Group, error)
	Leave(ctx context.Context, group domain.JID) error

	AddParticipants(ctx context.Context, group domain.JID, add []domain.JID) error
	RemoveParticipants(ctx context.Context, group domain.JID, remove []domain.JID) error
	Promote(ctx context.Context, group domain.JID, jids []domain.JID) error
	Demote(ctx context.Context, group domain.JID, jids []domain.JID) error

	Edit(ctx context.Context, group domain.JID, patch domain.GroupPatch) error

	InviteGet(ctx context.Context, group domain.JID) (domain.GroupInviteLink, error)
	InviteRevoke(ctx context.Context, group domain.JID) (domain.GroupInviteLink, error)
	InviteJoin(ctx context.Context, url string) (domain.Group, error)
}

// PollManager is the FR-032 port for poll send. Receive-side poll
// events are already wrapped by Tier 1 (poll.question + poll.options[]).
//
// Implementations MUST:
//   - Create sends a poll and returns its MessageID. selectable is the
//     number of options a voter may pick; 1 is single-choice.
//   - Vote returns -32000 upstream_error by contract. whatsmeow's
//     BuildPollVote requires the poll's full MessageInfo (chat, sender
//     and ID) plus the option NAMES, because the wire format hashes
//     names rather than carrying indices; this port's arguments
//     (chat, pollID, indices) cannot supply either. Widening the port
//     is a separate slice with its own message-store lookup.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type PollManager interface {
	Create(ctx context.Context, chat domain.JID, question string, options []string, selectable int) (domain.MessageID, error)
	Vote(ctx context.Context, chat domain.JID, pollID domain.MessageID, selected []int) error
}

// ForwardSender is the FR-033 extension surface on MessageSender for
// forwarding a message. Adapters that persist received-message bodies in
// messages.db implement this by looking up the source MessageID and
// synthesising an outbound Message of the same kind.
//
// Implementations MUST:
//   - Refuse a zero sourceID with a typed error.
//   - Return a non-zero domain.MessageID on success.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type ForwardSender interface {
	SendForward(ctx context.Context, to domain.JID, sourceChat domain.JID, sourceID domain.MessageID) (domain.MessageID, error)
}

// StarSender is the FR-033 extension surface on MessageSender for
// starring / unstarring a message. The two operations collapse onto a
// single method with a boolean because the wire protocol carries the
// same payload shape.
//
// Implementations MUST:
//   - Be idempotent: starring an already-starred message returns nil.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type StarSender interface {
	Star(ctx context.Context, chat domain.JID, id domain.MessageID, starred bool) error
}

// SessionTerminator is the FR-031 port for account-wide logout. LogoutAll
// unlinks every device from the account, not just this client.
//
// Implementations MUST:
//   - Surface the "upstream helper absent" condition as a typed error
//     routed to -32000 upstream_error (domain.ErrUpstreamError) so the
//     dispatcher can distinguish it from a transient wire failure.
//   - Write an AuditLogoutAll entry on every successful call (nothing
//     on failure — the audit is a success-only record per CLAUDE.md
//     rule 12, no silent fallbacks).
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type SessionTerminator interface {
	LogoutAll(ctx context.Context) error

	// Logout unlinks THIS device only (not the whole account). Used by
	// `wa pair --reset` (feature 110f) to clear the daemon's own ratchet
	// before initiating a fresh pair, without disturbing other phones /
	// desktops linked to the same WhatsApp account. Wraps whatsmeow
	// Client.Logout(ctx), which does the server-side IQ + local
	// Store.Delete in one transaction. messages.db is a separate SQLite
	// file (sqlitehistory package) — Store.Delete does NOT touch it.
	//
	// Implementations MUST:
	//   - Write an AuditLogout entry on success.
	//   - Return a "not paired" error when the daemon has no current device.
	//   - Honour ctx cancellation.
	Logout(ctx context.Context) error
}

// DisappearingSetter is the FR-033 (disappearing-messages) companion port
// for chat-level ephemeral-message timer configuration. Legal durations
// are the four values WhatsApp exposes: 0 (off), 86 400 (24 h),
// 604 800 (7 d), 7 776 000 (90 d). Any other value is rejected by the use
// case before reaching this port.
//
// Implementations MUST:
//   - Write an AuditDisappearingSet entry on every successful call.
//   - Honour ctx cancellation.
//   - Be safe for concurrent use.
type DisappearingSetter interface {
	SetDisappearing(ctx context.Context, chat domain.JID, seconds int) error
}
