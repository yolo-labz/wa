package rest

import (
	"encoding/json"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

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
	"messages.list":         ScopeRead,
	"groups":                ScopeRead,
	"groups.get":            ScopeRead,
	"contacts.lookup":       ScopeRead,
	"contacts.search":       ScopeRead,
	"contacts.list":         ScopeRead,
	"contacts.profilePhoto": ScopeRead,
	"contact.resolve":       ScopeRead,
	"contact.blocklist":     ScopeRead,
	"media.resolve":         ScopeRead,
	"media.download":        ScopeRead,
	"media.fetchBytes":      ScopeRead,
	"media.list":            ScopeRead,
	"draft.list":            ScopeRead,
	"draft.get":             ScopeRead,
	"webhook.list":          ScopeRead,
	"webhook.deliveries":    ScopeRead,
	"schedule.list":         ScopeRead,
	"labels.list":           ScopeRead,
	"embeddings.status":     ScopeRead,
	"privacy.get":           ScopeRead,
	"export":                ScopeRead,
	"chat.list":             ScopeRead,
	"sync.status":           ScopeRead,
	"system.hello":          ScopeRead,

	// SEND (read + outbound message + reaction + draft mutation)
	"send":                     ScopeSend,
	"sendMedia":                ScopeSend,
	"react":                    ScopeSend,
	"markRead":                 ScopeSend,
	"sendSeen":                 ScopeSend,
	"send.reply":               ScopeSend,
	"send.buttonResponse":      ScopeSend,
	"send.listResponse":        ScopeSend,
	"chat.composing":           ScopeSend,
	"presence.composing.start": ScopeSend,
	"presence.composing.stop":  ScopeSend,
	"presence.recording.start": ScopeSend,
	"presence.recording.stop":  ScopeSend,
	"draft.approve":            ScopeSend,
	// draft.create files a human-review proposal (feature 111 M1, MCP
	// draft-gate). It cannot transmit anything by itself — the send
	// happens at draft.approve — but it mutates daemon state and is the
	// agent's send-shaped verb, so it takes ScopeSend, not ScopeRead.
	"draft.create": ScopeSend,
	// webhook.replay re-POSTs an existing delivery to an
	// operator-approved endpoint — send-shaped, not admin.
	"webhook.replay":           ScopeSend,
	"draft.reject":             ScopeSend,
	"message.forward":          ScopeSend,
	"message.star":             ScopeSend,
	"message.setDisappearing":  ScopeSend,
	"contacts.annotate":        ScopeSend,
	"contacts.sync":            ScopeSend,
	"contacts.resolve.confirm": ScopeSend,
	"wait":                     ScopeSend,
	"media.gc":                 ScopeSend,
	"poll.create":              ScopeSend,
	"poll.vote":                ScopeSend,
	"sync.force":               ScopeSend,
	// media.upload is the synthetic method for the REST POST /media/upload
	// route (spec 198). It is NOT a dispatcher method — it exists only so the
	// upload handler's scope gate can reuse AllowedScope. Send-class: a remote
	// client staging bytes to send needs the same scope as sendMedia itself.
	"media.upload": ScopeSend,

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
	"session.logout":           ScopeAdmin,
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
	// Webhook endpoints are data-egress destinations: only admin
	// tokens may add or remove them (feature 112).
	"webhook.add":        ScopeAdmin,
	"webhook.remove":     ScopeAdmin,
	"admin.audit.rotate": ScopeAdmin,
	"history.purge":      ScopeAdmin,
	"search":             ScopeRead,
	"purge":              ScopeAdmin,

	// Composition-root methods registered via the dispatcherAdapter
	// intercept table (cmd/wad/main.go). Without these entries, even
	// an admin-scope token gets a 403 because AllowedScope fails
	// closed on unknown methods. Codex review §MINOR on PR 110d.
	"config.features":     ScopeRead,
	"debug.pprof.profile": ScopeAdmin,
}

// AllowedScope reports whether a token holding `granted` can invoke
// `method` with `params`. Returns false when the method is unknown — fail
// closed. Pass nil params when the caller has none; that never widens the
// bar, it only forgoes the one narrowing below.
func AllowedScope(method string, params json.RawMessage, granted MethodScope) bool {
	required, ok := RequiredScope(method, params)
	if !ok {
		return false
	}
	return granted >= required
}

// RequiredScope returns the minimum scope for (method, params), and false
// when the method is unclassified.
//
// It is MethodScopes[method] for every method, with exactly one narrowing:
// message.revoke carrying an explicit scope:"self" needs only ScopeSend.
//
// The two revoke scopes share a method name and nothing else. scope=everyone
// broadcasts a tombstone that every participant of a shared conversation
// acts on, irreversibly — admin, plainly. scope=self is a deleteMessageForMe
// app-state mutation that no peer ever observes; its entire blast radius is
// the caller's own view of their own account. Holding those to one bar meant
// any client that needed to hide a message for itself — an inbox filter, a
// mute-by-sender rule — had to be handed a token that can also call
// session.logout, pair, allow and group.removeParticipants. That is the
// escalation this split removes.
//
// Fails closed in every ambiguous direction. Absent, malformed, or any value
// other than the exact token "self" keeps the admin bar — absent in
// particular MUST stay admin, because the dispatcher reads an omitted scope
// as "everyone" (app.revokeParams, method_moderate.go). Parsing goes through
// domain.ParseRevokeScope rather than a local string compare so the gate and
// the dispatcher cannot drift on casing or padding: "Self", "SELF" and
// " self" are rejected by both, from the same code.
func RequiredScope(method string, params json.RawMessage) (MethodScope, bool) {
	required, ok := MethodScopes[method]
	if !ok {
		return 0, false
	}
	if method == revokeMethod && revokeIsSelfScoped(params) {
		return ScopeSend, true
	}
	return required, true
}

// revokeMethod is named rather than inlined so the narrowing above and the
// test that pins it cannot disagree about which method is special.
const revokeMethod = "message.revoke"

// revokeIsSelfScoped reports whether params explicitly select the self
// scope. Every failure path returns false, which keeps the admin bar.
func revokeIsSelfScoped(params json.RawMessage) bool {
	if len(params) == 0 {
		return false
	}
	var p struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return false
	}
	if p.Scope == "" {
		// Omitted scope is "everyone" at the dispatcher. Never narrow it.
		return false
	}
	sc, err := domain.ParseRevokeScope(p.Scope)
	return err == nil && sc == domain.RevokeSelf
}
