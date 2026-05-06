package rest

// MethodScope is the per-method permission level enforced on inbound
// REST requests when a scoped Authenticator (sqlitetokens) is wired.
// Spec 110d.
//
// Three coarse levels: read, send, admin. The mapping mirrors the
// allowlist's Action coarseness: a `read` token can fetch state but
// cannot send messages or mutate config; a `send` token adds the
// outbound message + reaction surface; `admin` opens the full
// dispatcher (allowlist edits, pairing, blocklist, profile edits,
// etc.).
//
// A method MUST appear in exactly one of MethodScopes. The unit test
// at scope_test.go pins this — adding a new method without classifying
// it fails the build.
type MethodScope int

// MethodScope values, ordered low→high so a higher scope satisfies
// every lower MethodScope check via `>=`.
const (
	ScopeRead  MethodScope = 1
	ScopeSend  MethodScope = 2
	ScopeAdmin MethodScope = 3
)

// MethodScopes maps every dispatcher method to the minimum scope
// required to invoke it. Tokens with a higher scope are accepted —
// the comparison is `tokenScope >= MethodScopes[method]`.
var MethodScopes = map[string]MethodScope{
	// READ
	"status":                ScopeRead,
	"health":                ScopeRead,
	"messages":              ScopeRead,
	"history":               ScopeRead,
	"thread.get":            ScopeRead,
	"messages.search":       ScopeRead,
	"groups":                ScopeRead,
	"groups.get":            ScopeRead,
	"contacts.lookup":       ScopeRead,
	"contacts.search":       ScopeRead,
	"contacts.profilePhoto": ScopeRead,
	"contact.resolve":       ScopeRead,
	"contact.blocklist":     ScopeRead,
	"media.resolve":         ScopeRead,
	"media.download":        ScopeRead,
	"draft.list":            ScopeRead,
	"draft.get":             ScopeRead,
	"schedule.list":         ScopeRead,
	"labels.list":           ScopeRead,
	"embeddings.status":     ScopeRead,
	"privacy.get":           ScopeRead,
	"export":                ScopeRead,
	"system.hello":          ScopeRead,

	// SEND (read + outbound message + reaction + draft mutation)
	"send":                     ScopeSend,
	"sendMedia":                ScopeSend,
	"react":                    ScopeSend,
	"markRead":                 ScopeSend,
	"send.reply":               ScopeSend,
	"chat.composing":           ScopeSend,
	"presence.composing.start": ScopeSend,
	"presence.composing.stop":  ScopeSend,
	"presence.recording.start": ScopeSend,
	"presence.recording.stop":  ScopeSend,
	"draft.approve":            ScopeSend,
	"draft.reject":             ScopeSend,
	"message.forward":          ScopeSend,
	"message.star":             ScopeSend,
	"message.setDisappearing":  ScopeSend,
	"contacts.annotate":        ScopeSend,
	"contacts.sync":            ScopeSend,
	"contacts.resolve.confirm": ScopeSend,
	"wait":                     ScopeSend,
	"media.gc":                 ScopeSend,
	"poll.vote":                ScopeSend,

	// ADMIN — pairing, allowlist, blocklist, group/profile mutation,
	// schedule writes, label management, message-revoke/edit, etc.
	"pair":                     ScopeAdmin,
	"panic":                    ScopeAdmin,
	"allow":                    ScopeAdmin,
	"contact.block":            ScopeAdmin,
	"contact.unblock":          ScopeAdmin,
	"chat.archive":             ScopeAdmin,
	"chat.mute":                ScopeAdmin,
	"chat.pin":                 ScopeAdmin,
	"chat.markUnread":          ScopeAdmin,
	"privacy.set":              ScopeAdmin,
	"session.logoutAll":        ScopeAdmin,
	"profile.setName":          ScopeAdmin,
	"profile.setStatus":        ScopeAdmin,
	"group.create":             ScopeAdmin,
	"group.leave":              ScopeAdmin,
	"group.addParticipants":    ScopeAdmin,
	"group.removeParticipants": ScopeAdmin,
	"group.promote":            ScopeAdmin,
	"group.demote":             ScopeAdmin,
	"group.edit":               ScopeAdmin,
	"group.inviteGet":          ScopeAdmin,
	"group.inviteRevoke":       ScopeAdmin,
	"group.inviteJoin":         ScopeAdmin,
	"message.revoke":           ScopeAdmin,
	"message.edit":             ScopeAdmin,
	"schedule.send":            ScopeAdmin,
	"schedule.cancel":          ScopeAdmin,
	"schedule.update":          ScopeAdmin,
	"labels.create":            ScopeAdmin,
	"labels.delete":            ScopeAdmin,
	"labels.assign":            ScopeAdmin,
	"labels.unassign":          ScopeAdmin,
	"embeddings.purge":         ScopeAdmin,
	"admin.reload":             ScopeAdmin,
	"admin.audit.rotate":       ScopeAdmin,
	"history.purge":            ScopeAdmin,
	"search":                   ScopeRead,
	"purge":                    ScopeAdmin,
}

// AllowedScope reports whether a token holding `granted` can invoke
// `method`. Returns false when the method is unknown — fail closed.
func AllowedScope(method string, granted MethodScope) bool {
	required, ok := MethodScopes[method]
	if !ok {
		return false
	}
	return granted >= required
}
