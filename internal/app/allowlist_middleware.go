package app

import (
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// methodActions maps every Tier-2 mutating JSON-RPC method to the named
// domain.Action that the allowlist middleware consults (FR-050). Read-only
// methods (contacts.blocklist, privacy.get, group.inviteGet, etc.) are
// intentionally absent: they bypass allowlist per FR-050.
//
// Global mutations that have no per-JID target (privacy.set, profile.*,
// session.logoutAll) are recorded here for coverage but use the zero JID as
// the gate key. The composition root is expected to grant actions on the
// zero JID when the operator wants the account owner to run those verbs.
var methodActions = map[string]domain.Action{
	// Per-JID targeted mutations.
	"send":                     domain.ActionSend,
	"sendMedia":                domain.ActionSend,
	"send.reply":               domain.ActionSend,
	"react":                    domain.ActionSend,
	"markRead":                 domain.ActionRead,
	"message.revoke":           domain.ActionRevoke,
	"message.edit":             domain.ActionEdit,
	"message.forward":          domain.ActionSend,
	"message.star":             domain.ActionSend,
	"message.setDisappearing":  domain.ActionChatState,
	"chat.archive":             domain.ActionChatState,
	"chat.mute":                domain.ActionChatState,
	"chat.pin":                 domain.ActionChatState,
	"chat.markUnread":          domain.ActionChatState,
	"chat.composing":           domain.ActionPresence,
	"presence.composing.start": domain.ActionPresence,
	"presence.composing.stop":  domain.ActionPresence,
	"presence.recording.start": domain.ActionPresence,
	"presence.recording.stop":  domain.ActionPresence,
	"contact.block":            domain.ActionBlock,
	"contact.unblock":          domain.ActionBlock,
	"group.create":             domain.ActionGroupCreate,
	"group.leave":              domain.ActionGroupLeave,
	"group.addParticipants":    domain.ActionGroupAdd,
	"group.removeParticipants": domain.ActionGroupRemove,
	"group.promote":            domain.ActionGroupPromote,
	"group.demote":             domain.ActionGroupDemote,
	"group.edit":               domain.ActionGroupEdit,
	"group.inviteRevoke":       domain.ActionGroupInvite,
	"group.inviteJoin":         domain.ActionGroupInvite,
	"poll.create":              domain.ActionSend,
	"poll.vote":                domain.ActionSend,
	// Global mutations (gated on domain.JID{} — the zero JID sentinel).
	"privacy.set":       domain.ActionPrivacySet,
	"profile.setName":   domain.ActionProfileEdit,
	"profile.setStatus": domain.ActionProfileEdit,
	"session.logoutAll": domain.ActionLogoutAll,
	"session.logout":    domain.ActionLogout,
}

// ActionForMethod returns the named allowlist Action for method. The
// second return is false for read-only methods (no allowlist gate) and for
// methods unknown to the registry.
func ActionForMethod(method string) (domain.Action, bool) {
	a, ok := methodActions[method]
	return a, ok
}

// pluginBoundaryRefused is the set of method names that the wa-assistant
// MCP shim MUST refuse when the invoking context is a `<channel
// source="wa">` message (FR-051). Daemon-side enforcement is defence in
// depth: the shim is primary. Kept here so plugin PRs can cite the
// authoritative list from this repo.
var pluginBoundaryRefused = map[string]struct{}{
	"contact.block":     {},
	"contact.unblock":   {},
	"privacy.set":       {},
	"group.leave":       {},
	"session.logoutAll": {},
	"session.logout":    {},
}

// IsPluginBoundaryRefused reports whether method must be refused when the
// invoking context is a `<channel source="wa">` envelope (FR-051).
func IsPluginBoundaryRefused(method string) bool {
	_, ok := pluginBoundaryRefused[method]
	return ok
}
