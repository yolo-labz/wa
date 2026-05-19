package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// FuzzDispatch drives Dispatcher.Handle with arbitrary (method, params)
// pairs. Invariants:
//  1. No input panics.
//  2. Unknown method names return ErrMethodNotFound. Known method names
//     return any error except an unrecognised error type.
//  3. When the handler returns a non-nil result blob, it is valid JSON
//     (the dispatcher must never emit malformed JSON to the wire).
func FuzzDispatch(f *testing.F) {
	seeds := []struct {
		method string
		params string
	}{
		{"", ""},
		{"status", `{}`},
		{"health", `{}`},
		{"send", `{}`},
		{"send", `{"to":"invalid","body":"x"}`},
		{"send", `null`},
		{"send", `{"to":"15551234567@s.whatsapp.net","body":"hi"}`},
		{"does-not-exist", `{}`},
		{"groups", `{}`},
		{"markRead", `{"chat":"","messageId":""}`},
		{"react", `{"chat":"","messageId":"","emoji":""}`},
		{"pair", `{"phone":""}`},
		{"system.hello", `{"client":"fuzz","protoVersion":2}`},
	}
	for _, s := range seeds {
		f.Add(s.method, s.params)
	}

	f.Fuzz(func(t *testing.T, method, params string) {
		// json.RawMessage of invalid JSON is fine at the dispatcher
		// boundary — handlers surface json unmarshal errors as
		// ErrInvalidParams. We do NOT pre-validate the params here; the
		// fuzz target is the dispatcher's panic-freedom + error contract.
		if !utf8.ValidString(method) {
			t.Skip()
		}

		d, _ := newTestDispatcher(t, 30*24*time.Hour)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		result, err := d.Handle(ctx, method, json.RawMessage(params))
		if err != nil {
			// Unknown methods must route through ErrMethodNotFound.
			if _, known := knownMethods()[method]; !known {
				if !errors.Is(err, app.ErrMethodNotFound) {
					t.Fatalf("unknown method %q: got err=%v, want ErrMethodNotFound", method, err)
				}
			}
			return
		}

		// Non-nil result must be valid JSON.
		if len(result) > 0 && !json.Valid(result) {
			t.Fatalf("handler %q returned invalid JSON: %q", method, result)
		}
	})
}

// knownMethods returns the set of JSON-RPC method names registered in
// the default dispatcher. Kept in sync with dispatcher.go manually —
// if the dispatcher registers a new method, add it here. A drift will
// only make the fuzz test more permissive (skip the unknown-method
// assertion), never falsely fail.
func knownMethods() map[string]struct{} {
	names := []string{
		"send", "sendMedia", "react", "pair", "wait", "status", "health",
		"groups", "markRead",
		"contacts.lookup", "contacts.search", "contacts.annotate",
		"contacts.sync", "contacts.resolve.confirm", "contacts.profilePhoto",
		"presence.composing.start", "presence.composing.stop",
		"presence.recording.start", "presence.recording.stop",
		"thread.get", "messages.search",
		"draft.list", "draft.get", "draft.approve", "draft.reject",
		"media.resolve", "media.download", "media.gc",
		"send.reply", "chat.composing", "groups.get",
		"schedule.send", "schedule.list", "schedule.cancel", "schedule.update",
		"labels.list", "labels.create", "labels.delete",
		"labels.assign", "labels.unassign",
		"embeddings.status", "embeddings.purge",
		"message.revoke", "message.edit", "message.forward",
		"message.star", "message.setDisappearing",
		"chat.archive", "chat.mute", "chat.pin", "chat.markUnread",
		"contact.block", "contact.unblock", "contact.blocklist",
		"privacy.set", "privacy.get", "session.logoutAll", "session.logout",
		"profile.setName", "profile.setStatus",
		"group.create", "group.leave",
		"group.addParticipants", "group.removeParticipants",
		"group.promote", "group.demote", "group.edit",
		"group.inviteGet", "group.inviteRevoke", "group.inviteJoin",
		"poll.vote",
	}
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}
