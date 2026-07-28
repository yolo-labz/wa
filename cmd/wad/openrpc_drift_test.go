package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/agentdocs"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// liveMethods builds a dispatcher the way cmd/wad does and returns every
// method name a client can call. Shared by both drift guards so the two
// directions can never be checked against different surfaces.
func liveMethods(t *testing.T) []string {
	t.Helper()

	mem := memory.New(nil)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: mem, Events: mem, Contacts: mem, Groups: mem, Session: mem,
		Allowlist: mem, Audit: mem, History: mem, Pairer: mem,
		SessionCreated: time.Now(), Logger: log,
	})
	t.Cleanup(func() { _ = d.Close() })
	// Adapter-registered methods (mirrors history_methods.go wiring).
	for _, m := range []string{
		"history", "messages", "search", "purge", "export",
		"chat.list", "messages.list", "media.list", "sync.force", "sync.status",
	} {
		d.RegisterMethod(m, nil)
	}
	// Socket-layer methods handled before dispatch.
	return append(d.Methods(), "system.hello")
}

// documentedMethods returns the method names published in openrpc.json,
// failing the test if the catalog is unreadable or implausibly small (a
// guard that parses to nothing would pass every assertion vacuously).
func documentedMethods(t *testing.T) []string {
	t.Helper()

	var doc struct {
		Methods []struct {
			Name string `json:"name"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(agentdocs.OpenRPCJSON, &doc); err != nil {
		t.Fatalf("openrpc.json invalid: %v", err)
	}
	if len(doc.Methods) < 25 {
		t.Fatalf("openrpc.json lists %d methods — catalog suspiciously small", len(doc.Methods))
	}
	names := make([]string, 0, len(doc.Methods))
	for _, m := range doc.Methods {
		names = append(names, m.Name)
	}
	return names
}

// TestOpenRPCMethodsExist pins the /openrpc.json catalog against the
// live dispatcher surface (feature 111 / roadmap 1.2): every method we
// document MUST exist, so the published contract can never describe a
// method the daemon would answer with -32601.
func TestOpenRPCMethodsExist(t *testing.T) {
	t.Parallel()

	live := liveMethods(t)
	for _, name := range documentedMethods(t) {
		if !slices.Contains(live, name) {
			t.Errorf("openrpc.json documents %q but the dispatcher does not register it", name)
		}
	}
}

// undocumentedMethods records the dispatcher methods openrpc.json does not
// publish, each with the reason. The catalog is deliberately narrower than
// the daemon — it is the surface an agent is expected to drive — but the
// gap was never written down, so it grew to two thirds of the dispatcher
// without anyone deciding it should. TestLiveMethodsAreCatalogued treats
// membership here as a decision; a method in neither this map nor the
// catalog is an undecided one, and fails the build.
//
// The right resolution for most of these is to document them, not to leave
// them here. Entries are grouped by the reason they are still outstanding
// so the backlog reads as a work list rather than an allowlist.
var undocumentedMethods = map[string]string{
	// Interactive/stateful flows whose contract is the CLI transcript, not
	// a single request/response pair — documenting them needs prose the
	// OpenRPC schema has nowhere to put.
	"pair":              "multi-frame QR/pairing-code flow; the daemon streams progress, so a params/result pair does not describe it",
	"session.logout":    "destructive session teardown; gated behind an explicit CLI confirmation rather than offered to agents",
	"session.logoutAll": "same, fleet-wide",
	"purge":             "destructive history deletion; CLI-confirmed, deliberately not an agent-reachable verb",

	// Covered by a sibling the catalog already publishes.
	"media.resolve":    "internal step of the catalogued media.download",
	"media.fetchBytes": "chunked transport of media.download, not a standalone verb",

	// Admin / operator surface: correct to keep out of the agent catalog.
	"media.gc":          "operator maintenance",
	"embeddings.purge":  "operator maintenance",
	"embeddings.status": "operator diagnostics",
	"contacts.sync":     "operator maintenance",

	// Undecided: ordinary agent-facing verbs with no reason to be missing.
	// These are the actual backlog — see the tracking note in openrpc.json.
	"react":                    "backlog: ordinary send-side verb, no reason to be undocumented",
	"history":                  "backlog",
	"messages":                 "backlog",
	"media.list":               "backlog",
	"contact.resolve":          "backlog",
	"contacts.resolve.confirm": "backlog: second leg of contact.resolve",
	"send.reply":               "backlog",
	"send.buttonResponse":      "backlog",
	"send.listResponse":        "backlog",
	"message.edit":             "backlog",
	"message.revoke":           "backlog",
	"message.forward":          "backlog",
	"message.star":             "backlog",
	"message.setDisappearing":  "backlog",
	"poll.vote":                "backlog",
	"schedule.update":          "backlog",
	"chat.archive":             "backlog",
	"chat.pin":                 "backlog",
	"chat.mute":                "backlog",
	"chat.markUnread":          "backlog",
	"chat.composing":           "backlog",
	"presence.composing.start": "backlog",
	"presence.composing.stop":  "backlog",
	"presence.recording.start": "backlog",
	"presence.recording.stop":  "backlog",
	"contact.block":            "backlog",
	"contact.unblock":          "backlog",
	"contact.blocklist":        "backlog",
	"contacts.list":            "backlog",
	"contacts.annotate":        "backlog",
	"contacts.profilePhoto":    "backlog",
	"group.create":             "backlog",
	"group.edit":               "backlog",
	"group.leave":              "backlog",
	"group.addParticipants":    "backlog",
	"group.removeParticipants": "backlog",
	"group.promote":            "backlog",
	"group.demote":             "backlog",
	"group.inviteGet":          "backlog",
	"group.inviteJoin":         "backlog",
	"group.inviteRevoke":       "backlog",
	"labels.list":              "backlog",
	"labels.create":            "backlog",
	"labels.delete":            "backlog",
	"labels.assign":            "backlog",
	"labels.unassign":          "backlog",
	"privacy.get":              "backlog",
	"privacy.set":              "backlog",
	"profile.setName":          "backlog",
	"profile.setStatus":        "backlog",
}

// TestLiveMethodsAreCatalogued is the reverse of TestOpenRPCMethodsExist.
// That guard only ever asserted documented ⊆ live, so a method could ship
// on the wire without ever reaching the published catalog — and 61 of the
// dispatcher's 94 had. Coverage stays intentionally partial, but the
// exclusions are now a recorded decision instead of an accident.
func TestLiveMethodsAreCatalogued(t *testing.T) {
	t.Parallel()

	documented := documentedMethods(t)
	live := liveMethods(t)
	if len(live) == 0 {
		t.Fatal("dispatcher registered zero methods — the guard would pass vacuously")
	}

	for _, name := range live {
		if slices.Contains(documented, name) {
			continue
		}
		if _, ok := undocumentedMethods[name]; !ok {
			t.Errorf("the dispatcher serves %q but internal/agentdocs/openrpc.json does not "+
				"publish it — add a methods[] entry, or record why it stays internal in "+
				"undocumentedMethods", name)
		}
	}
}

// TestUndocumentedMethodsAreHonest keeps the escape hatch from rotting: an
// entry must name a method the daemon still serves, must not claim a method
// is undocumented when the catalog publishes it, and must carry a reason.
func TestUndocumentedMethodsAreHonest(t *testing.T) {
	t.Parallel()

	documented := documentedMethods(t)
	live := liveMethods(t)

	for name, reason := range undocumentedMethods {
		if !slices.Contains(live, name) {
			t.Errorf("undocumentedMethods lists %q, which the dispatcher no longer serves — drop the entry", name)
		}
		if slices.Contains(documented, name) {
			t.Errorf("undocumentedMethods says %q is unpublished (%q) but openrpc.json documents it", name, reason)
		}
		if reason == "" {
			t.Errorf("undocumentedMethods entry %q has an empty reason", name)
		}
	}
}

// adapterParamTypes maps the documented methods this package registers
// to the struct their handler decodes. internal/app owns the rest and
// checks them in its own openrpc_params_test.go — each package has to
// do its own because the params structs are unexported.
var adapterParamTypes = map[string]any{
	"search":        searchParams{},
	"export":        exportParams{},
	"chat.list":     chatListParams{},
	"messages.list": messagesListParams{},
	"sync.force":    syncForceParams{},
}

// TestOpenRPCParamsMatchAdapterHandlers asserts the catalog names only
// parameters these handlers decode.
//
// TestOpenRPCMethodsExist above checks names and stops there, which is
// how openrpc.json came to document the legacy "search" method with the
// parameter set of "messages.search": the method existed, so the drift
// test passed, and an agent sending {phrase, chat} got -32602 from a
// handler that only reads "query".
func TestOpenRPCParamsMatchAdapterHandlers(t *testing.T) {
	t.Parallel()

	documented, err := agentdocs.ParamsByMethod()
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	for method, st := range adapterParamTypes {
		params, ok := documented[method]
		if !ok {
			t.Errorf("adapterParamTypes covers %q but openrpc.json no longer documents it", method)
			continue
		}
		typ := reflect.TypeOf(st)
		fields := map[string]bool{}
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" {
				fields[name] = true
			}
		}
		for _, p := range params {
			if !fields[p.Name] {
				t.Errorf("openrpc.json documents %q parameter %q, but %s has no such json field",
					method, p.Name, typ.Name())
			}
		}
	}
}
