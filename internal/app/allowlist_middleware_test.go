package app

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// readOnlyMethods are dispatcher methods that intentionally bypass the
// allowlist (FR-050). They read state or manage the client session without
// emitting anything over the WA wire that policy needs to gate.
var readOnlyMethods = map[string]struct{}{
	"pair":                     {},
	"status":                   {},
	"wait":                     {},
	"groups":                   {},
	"groups.get":               {},
	"contacts.lookup":          {},
	"contacts.search":          {},
	"contacts.annotate":        {},
	"contacts.sync":            {},
	"contacts.resolve.confirm": {},
	"contacts.profilePhoto":    {},
	"contacts.blocklist":       {}, // read-only blocklist enumeration
	"contact.blocklist":        {},
	"privacy.get":              {},
	"thread.get":               {},
	"messages.search":          {},
	"draft.list":               {},
	"draft.get":                {},
	"draft.approve":            {},
	"draft.reject":             {},
	"media.resolve":            {},
	"media.download":           {},
	"media.gc":                 {},
	"schedule.send":            {},
	"schedule.list":            {},
	"schedule.cancel":          {},
	"schedule.update":          {},
	"labels.list":              {},
	"labels.create":            {},
	"labels.delete":            {},
	"labels.assign":            {},
	"labels.unassign":          {},
	"embeddings.status":        {},
	"embeddings.purge":         {},
	"group.inviteGet":          {},
}

// TestAllowlistActionsCoverAllMutating asserts the methodActions registry
// (allowlist_middleware.go) is a superset of every dispatcher method that
// emits state-changing WA traffic and a subset of registered methods. This
// is the FR-050 completeness gate: any new mutating method must land in
// the registry or be explicitly exempted via readOnlyMethods.
func TestAllowlistActionsCoverAllMutating(t *testing.T) {
	d := &Dispatcher{}
	d.methods = make(map[string]methodHandler)

	// Enumerate the static method table via the same assignment shape as
	// dispatcher.go without instantiating adapters: we only need the keys.
	staticMethods := staticMethodNames()

	for _, name := range staticMethods {
		if _, exempt := readOnlyMethods[name]; exempt {
			if _, registered := methodActions[name]; registered {
				t.Errorf("%q is listed in readOnlyMethods AND methodActions — decide one", name)
			}
			continue
		}
		if _, ok := methodActions[name]; !ok {
			t.Errorf("dispatcher method %q is mutating but missing from methodActions (FR-050)", name)
		}
	}

	for method, action := range methodActions {
		if _, ok := findMethod(staticMethods, method); !ok {
			t.Errorf("methodActions has stale entry %q (no dispatcher handler registered)", method)
		}
		if !action.IsValid() {
			t.Errorf("methodActions[%q] = %v — not a valid domain.Action", method, action)
		}
	}
}

// TestPluginBoundaryRefusedListMembers asserts the FR-051 boundary set
// holds only methods the MCP shim refuses when invoked from a
// `<channel source="wa">` envelope. Every entry must also be a registered
// dispatcher method (otherwise the plugin's check would silently miss).
func TestPluginBoundaryRefusedListMembers(t *testing.T) {
	expected := []string{
		"contact.block",
		"contact.unblock",
		"privacy.set",
		"group.leave",
		"session.logoutAll",
	}
	if len(pluginBoundaryRefused) != len(expected) {
		t.Fatalf("pluginBoundaryRefused has %d entries, want %d", len(pluginBoundaryRefused), len(expected))
	}
	static := staticMethodNames()
	for _, m := range expected {
		if !IsPluginBoundaryRefused(m) {
			t.Errorf("IsPluginBoundaryRefused(%q) = false, want true", m)
		}
		if _, ok := findMethod(static, m); !ok {
			t.Errorf("pluginBoundaryRefused contains %q which is not a registered dispatcher method", m)
		}
	}
}

// TestActionForMethod spot-checks the registry lookup — revoke/edit/chat
// state use the named action rather than the legacy ActionSend blanket.
func TestActionForMethod(t *testing.T) {
	cases := map[string]domain.Action{
		"message.revoke":          domain.ActionRevoke,
		"message.edit":            domain.ActionEdit,
		"chat.archive":            domain.ActionChatState,
		"chat.composing":          domain.ActionPresence,
		"presence.composing.stop": domain.ActionPresence,
		"message.setDisappearing": domain.ActionChatState,
		"contact.block":           domain.ActionBlock,
		"group.leave":             domain.ActionGroupLeave,
	}
	for method, want := range cases {
		got, ok := ActionForMethod(method)
		if !ok {
			t.Errorf("ActionForMethod(%q) missing", method)
			continue
		}
		if got != want {
			t.Errorf("ActionForMethod(%q) = %v, want %v", method, got, want)
		}
	}
	if _, ok := ActionForMethod("status"); ok {
		t.Error("ActionForMethod(\"status\") returned true, want false (read-only)")
	}
}

// staticMethodNames returns the set of JSON-RPC method names the dispatcher
// registers at construction. Kept in-sync with dispatcher.go by hand; the
// coverage test above will flag drift if a new mutating method is added to
// dispatcher.go but not reflected here.
func staticMethodNames() []string {
	return []string{
		"send", "sendMedia", "react", "pair", "wait", "status", "groups",
		"markRead", "contacts.lookup", "contacts.search", "contacts.annotate",
		"contacts.sync", "contacts.resolve.confirm",
		"presence.composing.start", "presence.composing.stop",
		"presence.recording.start", "presence.recording.stop",
		"thread.get", "messages.search", "draft.list", "draft.get",
		"draft.approve", "draft.reject", "media.resolve", "media.download",
		"media.gc", "send.reply", "chat.composing", "groups.get",
		"schedule.send", "schedule.list", "schedule.cancel", "schedule.update",
		"labels.list", "labels.create", "labels.delete", "labels.assign",
		"labels.unassign", "embeddings.status", "embeddings.purge",
		"message.revoke", "message.edit", "message.forward", "message.star",
		"message.setDisappearing", "chat.archive", "chat.mute", "chat.pin",
		"chat.markUnread", "contact.block", "contact.unblock",
		"contact.blocklist", "privacy.set", "privacy.get", "session.logoutAll",
		"profile.setName", "profile.setStatus", "contacts.profilePhoto",
		"group.create", "group.leave", "group.addParticipants",
		"group.removeParticipants", "group.promote", "group.demote",
		"group.edit", "group.inviteGet", "group.inviteRevoke",
		"group.inviteJoin", "poll.vote",
	}
}

func findMethod(list []string, name string) (int, bool) {
	for i, m := range list {
		if m == name {
			return i, true
		}
	}
	return 0, false
}
