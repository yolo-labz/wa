package main

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestDispatcherMethodsAllScoped pins issue #158 — every method
// registered on *app.Dispatcher MUST have a rest.MethodScopes entry.
// Without this assertion, a new handler can land in app/ unnoticed and
// every REST caller hits the -32099 default-deny path because
// rest.AllowedScope fails closed on unknown methods. PR #153 ➜ issue
// #150 was the original example.
//
// Adapter-registered methods (history, messages, search, purge,
// export) are added via Dispatcher.RegisterMethod from cmd/wad's
// history wiring; we mirror that here so the test covers the same
// surface the daemon actually exposes. Composition-root intercepts
// (admin.reload, admin.audit.rotate, config.features,
// debug.pprof.profile) live in the dispatcherAdapter intercept table
// and are checked manually against rest.MethodScopes below.
func TestDispatcherMethodsAllScoped(t *testing.T) {
	t.Parallel()

	mem := memory.New(nil)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	d := app.NewDispatcher(app.DispatcherConfig{
		Sender:         mem,
		Events:         mem,
		Contacts:       mem,
		Groups:         mem,
		Session:        mem,
		Allowlist:      mem,
		Audit:          mem,
		History:        mem,
		Pairer:         mem,
		SessionCreated: time.Now(),
		Logger:         log,
	})
	t.Cleanup(func() { _ = d.Close() })

	// Adapter-registered dispatcher methods (mirror history_methods.go).
	adapterMethods := []string{
		"history",
		"messages",
		"search",
		"purge",
		"export",
		"chat.list",
		"messages.list",
		"media.list",
		"sync.force",
		"sync.status",
	}
	for _, m := range adapterMethods {
		d.RegisterMethod(m, nil)
	}

	for _, method := range d.Methods() {
		if _, ok := rest.MethodScopes[method]; !ok {
			t.Errorf("dispatcher method %q has no rest.MethodScopes entry — REST callers will hit -32099 default-deny (see issue #158)", method)
		}
	}

	// Composition-root intercept methods — present in cmd/wad/main.go's
	// dispatcherAdapter intercept table, not on *app.Dispatcher.
	interceptMethods := []string{
		"admin.reload",
		"admin.audit.rotate",
		"config.features",
		"debug.pprof.profile",
	}
	for _, m := range interceptMethods {
		if _, ok := rest.MethodScopes[m]; !ok {
			t.Errorf("intercept method %q has no rest.MethodScopes entry — REST callers will hit -32099 default-deny", m)
		}
	}
}
