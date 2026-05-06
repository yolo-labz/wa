package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yolo-labz/wa/v2/internal/observability"
)

// initRuntime resolves the active profile and initialises the runtime
// surfaces that must exist BEFORE any adapter or store opens —
// observability (OTel exporter) and crash-output (SetCrashOutput).
// Spec 016 M-023 / T077 — extracts the pre-store initialisation phase
// of run().
//
// Returns:
//   - resolver: the per-profile path resolver
//   - otelShutdown: deferred-Close handle for the OTel pipeline. ALWAYS
//     non-nil on success; the caller MUST install a defer that runs it
//     with a 5 s context timeout.
//   - crashClose: deferred-Close handle for the SetCrashOutput file.
//     May be nil — failure to open the crashes/ file is WARN-only,
//     debug.SetCrashOutput no-ops when unset.
//   - err: profile-resolve or observability-init failure (fatal).
//
// Note: autoMigrate is NOT called here. It depends on the resolver but
// its failure must run AFTER the otel/crash defers — the caller wires
// those defers immediately on initRuntime success and only THEN
// invokes autoMigrate.
func initRuntime(log *slog.Logger) (resolver *PathResolver, otelShutdown func(context.Context) error, crashClose func() error, err error) {
	profile := resolveDaemonProfile()
	resolver, err = NewPathResolver(profile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("profile %q: %w", profile, err)
	}
	log.Info("wad profile resolved", "profile", resolver.Profile())

	// Feature 018 Tier 3 — FR-037: init OTel exporter before any adapter
	// so spans/metrics from startup (migrate, store open, pair) are
	// captured. Required=true aborts startup on exporter failure; the
	// default (Required=false) WARNs and degrades to a no-op provider.
	otelShutdown, err = observability.Init(context.Background(),
		observability.ConfigFromEnv(resolver.Profile(), resolver.WadLog()))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("observability init: %w", err)
	}

	// Feature 018 T3-05 / FR-039: open the per-profile crashes/ file and
	// hand it to runtime/debug.SetCrashOutput so SIGABRT+panic dumps land
	// on disk. Sweep retention (≤10 files AND ≤100 MiB) happens here.
	// Failure is WARN-only; debug.SetCrashOutput already no-ops when
	// unset, matching the pre-018 behaviour.
	crashClose, err = observability.SetupCrashOutput(resolver.CrashesDir())
	if err != nil {
		log.Warn("crash output setup failed — crash dumps disabled", "err", err)
		crashClose = nil
		err = nil //nolint:wastedassign // explicit reset; documented in next return.
	}

	return resolver, otelShutdown, crashClose, nil
}
