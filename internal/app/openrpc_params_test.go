package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// dispatcherParamTypes maps every openrpc.json method this package
// handles to the struct its handler decodes the params into.
//
// The table is hand-maintained because the handlers keep their params
// structs unexported and reachable only from inside this package —
// which is also why this test lives here rather than next to the
// catalog. Adding a documented method without adding it here fails
// TestOpenRPCParamsMatchHandlers, so the table cannot silently rot.
var dispatcherParamTypes = map[string]any{
	"send":               sendParams{},
	"sendMedia":          sendMediaParams{},
	"markRead":           markReadParams{},
	"sendSeen":           sendSeenParams{},
	"wait":               waitParams{},
	"messages.search":    messagesSearchParams{},
	"thread.get":         threadGetParams{},
	"contacts.search":    contactsSearchParams{},
	"contacts.lookup":    contactsLookupParams{},
	"groups.get":         groupsGetParams{},
	"draft.create":       draftCreateParams{},
	"draft.list":         draftListParams{},
	"draft.get":          draftGetParams{},
	"draft.approve":      draftDecisionParams{},
	"draft.reject":       draftDecisionParams{},
	"schedule.send":      scheduleSendParams{},
	"schedule.list":      scheduleListParams{},
	"schedule.cancel":    scheduleIDParams{},
	"media.download":     mediaDownloadParams{},
	"webhook.add":        webhookAddParams{},
	"webhook.remove":     webhookIDParams{},
	"webhook.deliveries": webhookDeliveriesParams{},
	"webhook.replay":     webhookIDParams{},
}

// externalParamMethods are documented methods whose params are decoded
// outside this package, so their structs are unreachable from here.
// cmd/wad registers the history- and sync-backed handlers and asserts
// the same thing over them in TestOpenRPCParamsMatchAdapterHandlers;
// system.hello is answered by the socket handshake before dispatch and
// is pinned by the accept tests in that package.
var externalParamMethods = map[string]bool{
	"system.hello":  true,
	"search":        true,
	"chat.list":     true,
	"messages.list": true,
	"export":        true,
	"sync.force":    true,
}

// TestOpenRPCParamsMatchHandlers asserts that every parameter the
// published catalog names is a field the handler actually decodes.
//
// A method-name check is not enough: openrpc.json once documented
// markRead's parameter as "messageIds" (an array) when the handler read
// "messageId" (a string), and documented the legacy "search" method
// with the parameter set of "messages.search". Both cost an agent that
// followed the contract a -32602 with nothing to debug from.
func TestOpenRPCParamsMatchHandlers(t *testing.T) {
	t.Parallel()

	documented, err := agentdocs.ParamsByMethod()
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	for method, params := range documented {
		if len(params) == 0 || externalParamMethods[method] {
			continue
		}
		st, ok := dispatcherParamTypes[method]
		if !ok {
			t.Errorf("openrpc.json documents %q with parameters but no entry exists in dispatcherParamTypes — add one so its params stay checked", method)
			continue
		}
		fields := jsonFieldNames(reflect.TypeOf(st))
		for _, p := range params {
			if !fields[p.Name] {
				t.Errorf("openrpc.json documents %q parameter %q, but %s has no such json field (has %v)",
					method, p.Name, reflect.TypeOf(st).Name(), sortedKeys(fields))
			}
		}
	}
}

// jsonFieldNames returns the set of json tag names on a params struct,
// following embedded structs so a shared params base still counts.
func jsonFieldNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for f := range t.Fields() {
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			for k := range jsonFieldNames(f.Type) {
				out[k] = true
			}
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
