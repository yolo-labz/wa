// Package main is the wad daemon composition root. It wires all
// secondary adapters, the use case layer, and the socket primary adapter
// into a running daemon process. No business logic lives here.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/contactmirror"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitewebhooks"
	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// moderatorPort returns the adapter as an app.MessageModerator, flattening
// a typed-nil *whatsmeow.Moderator into a genuine interface nil. Otherwise
// the dispatcher's `d.moderator == nil` guard fires only for the
// untyped-nil case and method.revoke/edit would call into a nil method
// receiver.
func moderatorPort(m *wmAdapter.Moderator) app.MessageModerator {
	if m == nil {
		return nil
	}
	return m
}

// chatStatePort flattens a typed-nil *whatsmeow.ChatStateAdapter into a
// genuine interface-nil so the dispatcher's method_not_found guard fires
// correctly when the adapter failed to construct.
func chatStatePort(c *wmAdapter.ChatStateAdapter) app.ChatStateManager {
	if c == nil {
		return nil
	}
	return c
}

// blockerPort flattens a typed-nil *whatsmeow.BlockerAdapter into a genuine
// interface-nil so the dispatcher's contact.block/unblock/blocklist guard
// fires method_not_found when the adapter failed to construct.
func blockerPort(b *wmAdapter.BlockerAdapter) app.Blocker {
	if b == nil {
		return nil
	}
	return b
}

// privacyPort flattens a typed-nil *whatsmeow.PrivacyAdapter into a genuine
// interface-nil so the dispatcher's privacy.set/get guard fires
// method_not_found when the adapter failed to construct.
func privacyPort(p *wmAdapter.PrivacyAdapter) app.PrivacySettings {
	if p == nil {
		return nil
	}
	return p
}

// logoutAllPort flattens a typed-nil *whatsmeow.LogoutAllAdapter into a
// genuine interface-nil so the dispatcher's session.logoutAll guard fires
// method_not_found when the adapter failed to construct.
func logoutAllPort(l *wmAdapter.LogoutAllAdapter) app.SessionTerminator {
	if l == nil {
		return nil
	}
	return l
}

// profileEditorPort flattens a typed-nil *whatsmeow.ProfileAdapter into a
// genuine interface-nil so the dispatcher's profile.setName/setStatus/avatar
// guard fires method_not_found when the adapter failed to construct.
func profileEditorPort(p *wmAdapter.ProfileAdapter) app.ProfileEditor {
	if p == nil {
		return nil
	}
	return p
}

// groupAdminPort flattens a typed-nil *whatsmeow.GroupAdminAdapter into a
// genuine interface-nil so the dispatcher's group.* guard fires
// method_not_found when the adapter failed to construct.
func groupAdminPort(g *wmAdapter.GroupAdminAdapter) app.GroupAdmin {
	if g == nil {
		return nil
	}
	return g
}

// pollsPort flattens a typed-nil *whatsmeow.PollManagerAdapter into a genuine
// interface-nil so the dispatcher's poll.vote guard fires method_not_found
// when the adapter failed to construct.
func pollsPort(p *wmAdapter.PollManagerAdapter) app.PollManager {
	if p == nil {
		return nil
	}
	return p
}

// version is the daemon's public semver, injected at build time via
//
//	go build -ldflags "-X main.version=vX.Y.Z" ./cmd/wad
//
// The `socket.system.hello` response echoes this value to every client per
// FR-012. Local `go build` without ldflags yields "dev", which the hello
// handler sanitises to "0.0.0" on the wire.
var version = "dev"

// commit is the short git SHA, injected via `-X main.commit={{.Commit}}`
// by GoReleaser. Empty in local go-build. Surfaced through the
// `buildInfo()` helper so runtime callers can log it without having to
// import "runtime/debug".
var commit = ""

// date is the commit timestamp (RFC 3339, not wall-clock `date`),
// injected via `-X main.date={{.CommitDate}}`. Keeping this as the
// commit timestamp — never the wall clock — preserves the
// reproducible-build invariant per the yolo-labz release-engineering
// standard (`~/NixOS/meta/yolo-labz-release-engineering-plan.md`).
var date = ""

// resolveVersion prefers the ldflag-injected `version` value but falls
// back to `runtime/debug.BuildInfo.Main.Version` when the binary was
// built without ldflags (e.g. via `go install`). Closes the
// `wa version dev` reporting gap for go-install consumers.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// buildInfo returns a single-line "version (commit @ date)" string
// for diagnostic use. Safe to call at any time.
func buildInfo() string {
	v := resolveVersion()
	switch {
	case commit == "" && date == "":
		return v
	case commit != "" && date != "":
		return fmt.Sprintf("%s (%s @ %s)", v, commit, date)
	case commit != "":
		return fmt.Sprintf("%s (%s)", v, commit)
	default:
		return fmt.Sprintf("%s (@ %s)", v, date)
	}
}

func main() {
	// Service management subcommands (install-service, uninstall-service)
	// are handled before the daemon starts.
	if handleServiceCommand() {
		return
	}
	// Feature 018 T3-05 / FR-039: `wad crash list` subcommand exits before
	// opening any store or socket — it only reads the crashes/ dir.
	if handleCrashCommand() {
		return
	}
	// Feature 018 T3-08 / FR-038: `wad reload` dials the live daemon and
	// issues admin.reload — it never starts a new daemon.
	if handleReloadCommand() {
		return
	}
	// Feature 018 T3-09 / FR-038: `wad audit rotate` dials the daemon and
	// issues admin.audit.rotate — atomic rename + fresh 0600 file.
	if handleAuditCommand() {
		return
	}
	// Spec 110d: `wad token issue|revoke|list|sweep` operates directly
	// on the tokens.db file without starting the daemon.
	if handleTokenCommand() {
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wad: %v\n", err)
		os.Exit(1)
	}
}

// run is the daemon's composition root. It wires every adapter, store,
// and HTTP/JSON-RPC primary together in a strict order, registers the
// JSON-RPC method table, then blocks in serve() until the OS signals
// shutdown. Cyclomatic complexity is structurally large because this
// function IS the wiring graph — extracting steps changes nothing about
// the count of independent decision points and only adds indirection.
// PR #134 already extracted gracefulShutdown + initRuntime + openStores,
// and spec 195 (audit #12) extracted the Step 7a–7i port construction into
// buildPorts — yet run() still measures gocyclo 32 (> 15) because it
// remains the wiring graph (signals, feature flags, optional HTTP/REST,
// retention + soft-stale watchdogs); further mechanical decomposition makes
// the bring-up trace harder to audit (constitution §III.13 anti-scope-creep
// + §IV.21 Cockburn:
// composition roots are exempt from "small functions" heuristics by
// design). Tracked as T079 in spec 016; the nolint stays until a
// natural decomposition boundary surfaces.
//
//nolint:gocyclo // T079-deferred: composition root, see comment above + PR #134.
func run() error {
	// T017: parse --log-level / WA_LOG_LEVEL.
	level := parseLogLevel()
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	// Feature 009 — FR-037: set GOMEMLIMIT to prevent OOM from
	// malformed protobuf blobs. Default 512 MiB.
	debug.SetMemoryLimit(512 * 1024 * 1024)

	log.Info("wad starting", "build", buildInfo())

	// Spec 016 M-023 / T077: profile resolve + observability init +
	// crash-output setup extracted into initRuntime. Defers must live
	// in run() scope so they fire even on autoMigrate / store-open
	// failure paths below.
	resolver, otelShutdown, crashClose, err := initRuntime(log)
	if err != nil {
		return err
	}
	profile := resolver.Profile()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutCtx); err != nil {
			log.Warn("otel shutdown error", "err", err)
		}
	}()
	if crashClose != nil {
		defer func() {
			if err := crashClose(); err != nil {
				log.Warn("crash output close error", "err", err)
			}
		}()
	}

	// Feature 008: detect and perform legacy-layout migration BEFORE any
	// adapter construction. See contracts/migration.md §When the migration
	// runs and FR-015..FR-022.
	if err := autoMigrate(resolver, log); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Spec 016 M-023 / T078: steps 1-3b (directory + 6 SQLite stores)
	// extracted into openStores. The returned startupStores carries a
	// startupCleanup primed for incremental error-path teardown (T020),
	// so subsequent steps continue using the same cleanup instance via
	// stores.Cleanup.
	stores, err := openStores(context.Background(), resolver, log)
	if err != nil {
		return err
	}
	sessionStore := stores.SessionStore
	historyStore := stores.HistoryStore
	draftStore := stores.DraftStore
	scheduleStore := stores.ScheduleStore
	cleanup := stores.Cleanup
	sessionDBPath := resolver.SessionDB()
	historyDBPath := resolver.HistoryDB()

	// Step 4: open slogaudit (per-profile audit.log).
	auditLogPath := resolver.AuditLog()
	log.Info("opening audit log", "path", auditLogPath)
	auditLog, err := slogaudit.Open(auditLogPath)
	if err != nil {
		cleanup.run()
		return fmt.Errorf("slogaudit: %w", err)
	}
	cleanup.auditLog = auditLog

	// Step 5: load per-profile allowlist from allowlist.toml (or empty).
	allowlistPath := resolver.AllowlistTOML()
	log.Info("loading allowlist", "path", allowlistPath)
	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		cleanup.run()
		return fmt.Errorf("allowlist: %w", err)
	}

	// Step 5a (feature 017 / T3-01): load per-profile config.toml. Missing
	// file → DefaultFeatureFlags.
	configPath := resolver.ConfigTOML()
	log.Info("loading config", "path", configPath)
	cfg, err := loadConfig(configPath)
	if err != nil {
		cleanup.run()
		return fmt.Errorf("config: %w", err)
	}
	log.Info("feature flags resolved",
		"embeddings", cfg.Features.Embeddings,
		"scheduled_sends", cfg.Features.ScheduledSends,
		"labels", cfg.Features.Labels)

	// Step 6: start allowlist watcher goroutine.
	var allowlistMu sync.RWMutex
	watchCtx, watchCancel := context.WithCancel(context.Background())
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		if err := watchAllowlist(watchCtx, allowlistPath, allowlist, &allowlistMu, log); err != nil {
			log.Error("allowlist watcher exited with error", "err", err)
		}
	}()
	cleanup.watchCancel = watchCancel
	cleanup.watchDone = watchDone

	// Step 7: open whatsmeow adapter.
	log.Info("opening whatsmeow adapter")
	waAdapter, err := wmAdapter.Open(context.Background(), sessionStore, historyStore, allowlist, log)
	if err != nil {
		cleanup.run()
		return fmt.Errorf("whatsmeow: %w", err)
	}
	// Adapter now owns historyStore + sessionStore — see adapter.go:Close.
	cleanup.waAdapter = waAdapter
	cleanup.adapterOwnsStores = true

	// Wire R-07 panic-wipe paths so `panic` RPC / events.LoggedOut
	// can fully sanitise the profile on trigger.
	lockPath, lockPathErr := resolver.LockPath()
	if lockPathErr != nil {
		log.Warn("panic lockfile path unresolved; omitting from wipe", "err", lockPathErr)
	}
	waAdapter.SetPanicArtefacts(wmAdapter.PanicArtefacts{
		SessionDB: sessionDBPath,
		HistoryDB: historyDBPath,
		AuditLog:  auditLogPath,
		Lockfile:  lockPath,
	})
	waAdapter.SetProfile(resolver.Profile())

	// Step 7a–7i (spec 195 / audit #12): construct every optional whatsmeow
	// sub-adapter (media, labels, moderator, chat-state, blocker, privacy,
	// logout-all, profile, group-admin, polls) in a single cohesive helper.
	// buildPorts performs construction ONLY — no goroutine launch, no
	// shutdown wiring, no error-path store teardown — so lifecycle and
	// reverse-order shutdown stay entirely in run(). Every sub-adapter
	// degrades gracefully (log.Warn + nil) on failure, so buildPorts never
	// fails today; the error return is threaded for future fallibility.
	p, err := buildPorts(waAdapter, resolver, historyStore, log)
	if err != nil {
		cleanup.run()
		return err
	}
	mediaAdapter := p.media
	// Spec 198: wire the content-addressed store into the whatsmeow adapter so
	// the SHA256-source branch of sendMedia (a remote client referencing an
	// object it uploaded via POST /media/upload) can read the bytes off disk.
	// Guard the typed nil — passing a nil *MediaAdapter through the interface
	// would defeat SetMediaResolver's own nil check (non-nil interface, nil
	// pointer). When media is unavailable the seam stays disabled and SHA256
	// sends return -32xxx media_unsupported, which is the honest failure.
	if mediaAdapter != nil {
		waAdapter.SetMediaResolver(mediaAdapter)
	}
	labelsAdapter := p.labels
	moderatorAdapter := p.moderator
	chatStateAdapter := p.chatState
	blockerAdapter := p.blocker
	privacyAdapter := p.privacy
	logoutAllAdapter := p.logoutAll
	profileAdapter := p.profile
	groupAdminAdapter := p.groupAdmin
	pollsAdapter := p.polls

	// Step 8: construct app.Dispatcher with all 9 ports.
	//
	// FR-032: SessionCreated MUST be sourced from the persisted session
	// store, not from time.Now(). The previous bug hardcoded time.Now() which
	// reset the warmup multiplier to "day 0" on every daemon restart. When
	// the session is zero (not yet paired), we fall back to time.Now() and
	// the app layer will update it once pairing completes.
	log.Info("constructing dispatcher")
	sessionCreatedAt := time.Now()
	paired := false
	if existing, loadErr := waAdapter.Load(context.Background()); loadErr == nil && !existing.CreatedAt().IsZero() {
		sessionCreatedAt = existing.CreatedAt()
		paired = true
		log.Info("sourced SessionCreated from session store", "ts", sessionCreatedAt)
	} else {
		log.Info("session not yet paired, SessionCreated defaults to now", "ts", sessionCreatedAt)
	}

	// Detect business-account status (T3-22). Only meaningful once paired;
	// personal accounts get -32114 on labels.* regardless of the flag.
	isBusiness := false
	if paired {
		if detected, dErr := waAdapter.DetectBusiness(context.Background()); dErr == nil {
			isBusiness = detected
			log.Info("business-account status detected", "is_business", isBusiness)
		} else {
			log.Warn("business-account detection failed, defaulting to personal", "err", dErr)
		}
	}

	// Shared SafetyPipeline (FR-003, T1-10 R-03). Both the dispatcher and
	// the ScheduleFirer go through the SAME allowlist + rate-limit budget,
	// so a burst of schedule fires cannot crowd out CLI sends (or vice
	// versa) and warmup caps apply uniformly. Pre-018 the firer carried an
	// `if Safety != nil` bypass and main.go left Safety=nil — a latent
	// regression where scheduled sends skipped the allowlist entirely.
	rateLimiter := app.NewRateLimiter(sessionCreatedAt)
	safety := app.NewSafetyPipeline(allowlist, rateLimiter)

	// ScheduleFirer + ScheduleRunner (FR-111). Construction panics if
	// Safety/Store/Sender are nil — wiring drift fails at boot, not at
	// first fire.
	firer := app.NewScheduleFirer(app.ScheduleFirerConfig{
		Store:  scheduleStore,
		Drafts: draftStore,
		Sender: waAdapter,
		Safety: safety,
		Audit:  auditLog,
		Log:    log,
		Now:    time.Now,
	})
	scheduleRunner := app.NewScheduleRunner(scheduleStore, profile, firer.Fire)

	softStaleSec := ParseSoftStaleThresholdSec(os.Getenv("WA_SOFT_STALE_THRESHOLD_SEC"), log)

	transcriber, transcriberName, err := selectTranscriber(log)
	if err != nil {
		return fmt.Errorf("select transcriber: %w", err)
	}

	// Searchable contact mirror (#175/#181). The whatsmeow adapter implements
	// ContactDirectory but not ContactSearcher, so wiring it directly made
	// contacts.search/.annotate/.sync return -32601 and left contacts.db
	// empty. The mirror composes the live directory with the local
	// sqlitecontacts store and, on Sync, backfills it from the whatsmeow
	// contact store + message-history senders. When the best-effort
	// contacts.db failed to open (nil store) we fall back to the bare
	// adapter, preserving the documented graceful degradation.
	var contactsPort app.ContactDirectory = waAdapter
	if stores.ContactsStore != nil {
		contactsPort = contactmirror.New(
			waAdapter,
			stores.ContactsStore,
			log,
			contactmirror.ContactSourceFunc(waAdapter.AllContacts),
			contactmirror.ContactSourceFunc(historyStore.DistinctSenders),
		)
	}

	// Pre-send deliverability gate (app.ErrNotOnWhatsApp). Wired by
	// default; WA_DISABLE_ONWA_CHECK=1 is a kill-switch that disables the
	// gate without a redeploy (leaves nil → ensureOnWhatsApp skips).
	var onWhatsApp app.OnWhatsAppChecker = waAdapter
	if os.Getenv("WA_DISABLE_ONWA_CHECK") == "1" {
		onWhatsApp = nil
		log.Warn("on-WhatsApp pre-send gate DISABLED via WA_DISABLE_ONWA_CHECK")
	}
	dispatcher := app.NewDispatcher(app.DispatcherConfig{
		Sender:                waAdapter,
		OnWhatsApp:            onWhatsApp,
		Events:                waAdapter,
		Contacts:              contactsPort,
		Groups:                waAdapter,
		Session:               waAdapter,
		Allowlist:             allowlist,
		Audit:                 auditLog,
		History:               waAdapter,
		Pairer:                waAdapter,
		Drafts:                draftStore,
		Webhooks:              webhookStoreOrNil(stores.WebhookStore),
		Media:                 mediaAdapter,
		Scheduled:             scheduleStore,
		ScheduleRunner:        scheduleRunner,
		Labels:                labelsAdapter,
		Moderator:             moderatorPort(moderatorAdapter),
		ChatState:             chatStatePort(chatStateAdapter),
		Blocker:               blockerPort(blockerAdapter),
		Privacy:               privacyPort(privacyAdapter),
		SessionTerm:           logoutAllPort(logoutAllAdapter),
		ProfileEditor:         profileEditorPort(profileAdapter),
		GroupAdmin:            groupAdminPort(groupAdminAdapter),
		Polls:                 pollsPort(pollsAdapter),
		Identity:              waAdapter.NewIdentityResolver(),
		IsBusinessAccount:     isBusiness,
		Idempotency:           historyStore.IdempotencySidecar(),
		Transcriber:           transcriber,
		Transcripts:           historyStore.TranscriptStore(),
		TranscriberName:       transcriberName,
		Features:              cfg.Features,
		Profile:               profile,
		SessionCreated:        sessionCreatedAt,
		Logger:                log,
		Safety:                safety,
		Quoted:                sqlitehistory.NewQuotedMessageAdapter(historyStore),
		Websocket:             waAdapter,
		SoftStaleThresholdSec: softStaleSec,
	})

	// Step 8a (feature 009): wire known-recipient check for per-recipient
	// rate limiting (FR-032). The callback queries messages.db for prior
	// outbound messages without importing sqlitehistory in the app layer.
	dispatcher.SetKnownRecipientFunc(func(jid domain.JID) bool {
		msgs, err := historyStore.QueryHistory(context.Background(), jid.String(), "", 1)
		return err == nil && len(msgs) > 0
	})

	// Step 8a1 (feature 018 T2-21 / FR-028): wire pushName refresh so every
	// inbound message upserts the contacts mirror when whatsmeow surfaces a
	// fresh display name. The sink is the dispatcher's RefreshPushName,
	// which is a no-op if the contacts store failed to open.
	waAdapter.SetPushNameRefresher(dispatcher.RefreshPushName)

	// Step 8b (feature 009): register history/messages/search/purge/export
	// methods on the dispatcher. These query sqlitehistory.Store directly
	// (not through the HistoryStore port) for rich metadata per FR-023.
	registerHistoryMethods(dispatcher, historyStore, auditLog, log)

	// Step 8b1 (#174): expose sync visibility. sync.force triggers an
	// on-demand history pull (the daemon DB can lag behind the phone when
	// new messages arrive); sync.status surfaces the sync engine's
	// internal state (in-flight force round-trips, worker queue depth)
	// that the health probe does not. waAdapter is the concrete on-demand
	// history transport and is always non-nil here (Open failure is fatal
	// above).
	registerSyncMethods(dispatcher, waAdapter)

	// media.list (#173) needs both the history store (to enumerate
	// media-bearing rows) and the media adapter (to re-parse each row's
	// proto for sha/size/duration and probe the content-addressed cache).
	// Skipped when the media adapter is unavailable — the method then
	// default-denies like any unregistered method.
	if mediaAdapter != nil {
		registerMediaListMethod(dispatcher, historyStore, mediaAdapter)
	}

	// Step 8c (feature 018 T3-04 / FR-038): expose runtime pprof via
	// debug.pprof.profile on the unix socket. No net/http/pprof —
	// constitution §IV.21 forbids wad-opened TCP listeners.
	dispatcher.RegisterMethod("debug.pprof.profile", observability.PprofHandler)

	// Step 9: wire composition-root-level handlers for "allow" and "panic".
	// These methods need filesystem I/O and adapter access that the app
	// dispatcher cannot have, so they are intercepted before delegation.
	allowHandler := handleAllow(allowlist, &allowlistMu, allowlistPath, auditLog, log)
	panicHandler := handlePanic(waAdapter, waAdapter, auditLog, log)
	configFeaturesHandler := handleConfigFeatures(cfg.Features)
	adminReloadHandler := handleAdminReload(allowlist, &allowlistMu, allowlistPath, auditLog, log)
	adminAuditRotateHandler := handleAdminAuditRotate(auditLog, log)

	cleanup.dispatcher = dispatcher

	// Step 10: construct dispatcherAdapter (app.Event -> socket.Event bridge).
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	da := newDispatcherAdapter(bridgeCtx, dispatcher, map[string]compositionHandler{
		"allow":              allowHandler,
		"panic":              panicHandler,
		"config.features":    configFeaturesHandler,
		"admin.reload":       adminReloadHandler,
		"admin.audit.rotate": adminAuditRotateHandler,
	})
	cleanup.bridgeCancel = bridgeCancel
	cleanup.da = da

	// Step 11: construct socket.Server.
	server := socket.NewServer(da, log, socket.WithServerVersion(version))

	// Step 12: signal.NotifyContext for root context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Hoisted from below: every Close() in the shutdown sequence and
	// the startup-failure cleanup gets a 5-second budget per FR-040.
	const shutdownTimeout = 5 * time.Second
	cleanup.shutdownTimeout = shutdownTimeout

	// Step 12-bis (spec 109): optional HTTP health endpoint for
	// container probes. Activated by setting WAD_HEALTH_HTTP_ADDR
	// (e.g. ":8080") in the environment — unset = no listener,
	// zero behaviour change for non-container deploys.
	healthHTTP, err := startHealthHTTP(dispatcher.IsReady, log)
	if err != nil {
		log.Error("start health http", "err", err)
	}
	cleanup.healthHTTP = healthHTTP

	// Step 12-ter (spec 110a): optional REST primary adapter for
	// browser / mobile / agent clients that cannot use the unix
	// socket. Activated by setting BOTH WAD_REST_HTTP_ADDR and
	// WAD_REST_TOKEN. Refuses to start if the addr is set without a
	// token — fails closed rather than exposing an unauth daemon.
	// Wrap the media adapter in a genuine nil interface when it is
	// unavailable: passing a typed nil *MediaAdapter directly would
	// produce a non-nil app.MediaStore whose Resolve panics. Issue #169.
	var mediaStore app.MediaStore
	if mediaAdapter != nil {
		mediaStore = mediaAdapter
	}
	restShutdown, err := startRESTHTTP(ctx, da, dispatcher, mediaStore, log)
	if err != nil {
		// Refusing to start the REST adapter is a fatal misconfiguration
		// (operator set WAD_REST_HTTP_ADDR without a token, weak token,
		// public bind without WAD_REST_TRUST_PROXY, etc) — abort the
		// daemon rather than silently continue without REST.
		cleanup.run()
		return fmt.Errorf("start rest http: %w", err)
	}
	cleanup.restShutdown = restShutdown

	// Step 12-pre (feature 017): arm schedule-runner timers for any
	// pending rows persisted from a prior daemon run. Start MUST use the
	// daemon-lifetime ctx — timer callbacks capture it for fire dispatch.
	// Feature 112: start the webhook delivery worker when the store
	// opened. Endpoint-less profiles pay one 5s-tick query; acceptable.
	if stores.WebhookStore != nil {
		webhookWorker := app.NewWebhookWorker(stores.WebhookStore, dispatcher, profile, log)
		webhookWorker.Start(ctx)
		cleanup.webhookWorker = webhookWorker
		log.Info("webhook delivery worker started")
	}

	if cfg.Features.ScheduledSends {
		if err := scheduleRunner.Start(ctx); err != nil {
			log.Error("schedule runner start failed, scheduled sends disabled", "err", err)
		} else {
			log.Info("schedule runner started", "pending", scheduleRunner.Pending())
		}
	}
	cleanup.scheduleRunner = scheduleRunner

	// Resolve per-profile socket path.
	sockPath, err := resolver.SocketPath()
	if err != nil {
		cleanup.run()
		return fmt.Errorf("socket path: %w", err)
	}

	// Step 12b (spec 110g): start the soft-stale watchdog goroutine.
	// Detect-and-emit by default; when WA_SOFT_STALE_RECOVER is enabled
	// (spec 110g recover extension) a healthy->stale edge also forces a
	// websocket reconnect to break a zombie link. Disabled entirely when
	// WA_SOFT_STALE_THRESHOLD_SEC is unset, empty, "0", or non-numeric.
	// The goroutine exits when ctx is cancelled.
	if softStaleSec > 0 {
		deps := SoftStaleDeps{
			Bridge:       dispatcher.Bridge(),
			Probe:        waAdapter,
			Profile:      profile,
			ThresholdSec: softStaleSec,
			Log:          log,
		}
		if ParseSoftStaleRecover(os.Getenv("WA_SOFT_STALE_RECOVER")) {
			deps.Recover = waAdapter.Reconnect
			deps.BackoffCapSec = ParseSoftStaleBackoffCapSec(
				os.Getenv("WA_SOFT_STALE_BACKOFF_CAP_SEC"), softStaleSec, log,
			)
			log.Info("soft-stale recovery enabled (will force reconnect on stale)",
				"cooldownSec", recoverCooldownDefaultSec,
				"backoffCapSec", deps.BackoffCapSec)
			// Backfill extension (spec 110g, opt-in, requires recovery):
			// after the forced reconnect, pull the messages WhatsApp
			// delivered into the dead socket during the stall. Reconnect
			// re-opens the link but never recovers those on its own.
			if ParseSoftStaleBackfill(os.Getenv("WA_SOFT_STALE_BACKFILL")) {
				deps.Backfill = func(ctx context.Context) error {
					_, err := waAdapter.BackfillRecent(ctx)
					return err
				}
				log.Info("soft-stale backfill enabled (will pull missed messages after reconnect)")
			}
		} else if ParseSoftStaleBackfill(os.Getenv("WA_SOFT_STALE_BACKFILL")) {
			log.Warn("WA_SOFT_STALE_BACKFILL ignored: requires WA_SOFT_STALE_RECOVER=1")
		}
		go runSoftStaleWatchdog(ctx, deps)
	}

	// Step 12a (feature 009): start retention cleanup goroutine if configured.
	// Reads WA_RETENTION_DAYS env (default 0 = disabled).
	if retDays := os.Getenv("WA_RETENTION_DAYS"); retDays != "" {
		var days int
		if _, err := fmt.Sscanf(retDays, "%d", &days); err == nil && days > 0 {
			retention := time.Duration(days) * 24 * time.Hour
			go runRetentionCleanup(ctx, historyStore, waAdapter, retention, log)
			log.Info("retention cleanup enabled", "days", days)
		}
	}

	// Step 13: server.Run blocks until signal.
	log.Info("starting socket server", "path", sockPath)
	serverErr := server.Run(ctx, sockPath)

	// Step 14: shutdown in reverse order per FR-033/FR-040. Spec 016
	// M-023 / T080: extracted into startupCleanup.gracefulShutdown so
	// success-path and error-path teardown share the SAME ordering
	// contract (the LIFO sequence asserted by Codex review §MINOR on
	// PR #129). Each Close goes through closeWithTimeout — a hung
	// component cannot block the rest of the sequence.
	cleanup.gracefulShutdown()
	return serverErr
}

// ports bundles the optional whatsmeow sub-adapters constructed in the
// Step 7a–7i region (spec 195 / audit #12). Each field is a typed pointer
// that may be nil when its constructor failed — the dispatcher wiring in
// run() flattens typed-nils via the *Port helpers so the method_not_found
// guards fire correctly. This struct carries construction output only; it
// holds no lifecycle, goroutine, or shutdown state.
type ports struct {
	media      *wmAdapter.MediaAdapter
	labels     *wmAdapter.LabelsAdapter
	moderator  *wmAdapter.Moderator
	chatState  *wmAdapter.ChatStateAdapter
	blocker    *wmAdapter.BlockerAdapter
	privacy    *wmAdapter.PrivacyAdapter
	logoutAll  *wmAdapter.LogoutAllAdapter
	profile    *wmAdapter.ProfileAdapter
	groupAdmin *wmAdapter.GroupAdminAdapter
	polls      *wmAdapter.PollManagerAdapter
}

// buildPorts constructs the Step 7a–7i optional sub-adapters from the
// already-open whatsmeow adapter (spec 195 / audit #12). It is a PURE,
// behaviour-preserving extraction of the construction block formerly inline
// in run(): same order, same graceful-degradation semantics (log.Warn +
// nil on failure), no goroutine launches, no store teardown. Because every
// sub-adapter degrades gracefully, the returned error is always nil today;
// it is threaded so a future constructor can surface a fatal failure to
// run()'s teardown without reordering this block.
func buildPorts(waAdapter *wmAdapter.Adapter, resolver *PathResolver, historyStore *sqlitehistory.Store, log *slog.Logger) (ports, error) {
	var p ports

	// Step 7a (feature 017): construct media + labels adapters.
	// MediaAdapter holds the shared content-addressed cache root; it is
	// safe to proceed if the root mkdir fails — handlers surface
	// -32000 on first use rather than crashing the daemon.
	mediaAdapter, mErr := waAdapter.NewMediaAdapter(resolver.MediaRoot())
	if mErr != nil {
		log.Warn("media adapter unavailable, media.* methods will error", "err", mErr)
		mediaAdapter = nil
	}
	p.media = mediaAdapter
	p.labels = waAdapter.NewLabelsAdapter(nil)

	// Step 7b (feature 018 T2-05/T2-06): construct MessageModerator.
	// The moderator binds the whatsmeow client + sqlitehistory.Store so
	// Revoke/Edit can both emit tombstones/edits over the wire AND stamp
	// the local messages.db row (FR-014/FR-015). Failure is fatal — the
	// daemon would otherwise answer method_not_found for message.revoke.
	moderatorAdapter, mdErr := waAdapter.NewModeratorFor(historyStore)
	if mdErr != nil {
		log.Warn("moderator adapter unavailable, message.revoke/edit disabled", "err", mdErr)
		moderatorAdapter = nil
	}
	p.moderator = moderatorAdapter

	// Step 7c (feature 018 T2-07): construct ChatStateManager. Failure
	// leaves chat.archive/mute/pin/markUnread answering method_not_found.
	chatStateAdapter, csErr := waAdapter.NewChatStateFor()
	if csErr != nil {
		log.Warn("chat state adapter unavailable, chat.* methods disabled", "err", csErr)
		chatStateAdapter = nil
	}
	p.chatState = chatStateAdapter

	// Step 7d (feature 018 T2-09): construct Blocker. Failure leaves
	// contact.block/unblock/blocklist at method_not_found and the pre-send
	// gate in handleSend/handleSendMedia/handleReact/handleSendReply
	// no-ops (ensureNotBlocked returns nil when Blocker is nil).
	blockerAdapter, bkErr := waAdapter.NewBlockerFor()
	if bkErr != nil {
		log.Warn("blocker adapter unavailable, contact.block/unblock/blocklist disabled", "err", bkErr)
		blockerAdapter = nil
	}
	p.blocker = blockerAdapter

	// Step 7e (feature 018 T2-11): construct PrivacySettings. Failure
	// leaves privacy.set/privacy.get answering method_not_found.
	privacyAdapter, pvErr := waAdapter.NewPrivacyFor()
	if pvErr != nil {
		log.Warn("privacy adapter unavailable, privacy.set/get disabled", "err", pvErr)
		privacyAdapter = nil
	}
	p.privacy = privacyAdapter

	// Step 7f (feature 018 T2-12): construct SessionTerminator. The
	// LogoutAll helper is absent at the pinned whatsmeow commit, so the
	// adapter returns -32000 upstream_error until the upstream ships it.
	logoutAllAdapter, loErr := waAdapter.NewLogoutAllFor()
	if loErr != nil {
		log.Warn("logout-all adapter unavailable, session.logoutAll disabled", "err", loErr)
		logoutAllAdapter = nil
	}
	p.logoutAll = logoutAllAdapter

	// Step 7g (feature 018 T2-13): construct ProfileEditor. Avatar cache
	// lives under $XDG_CACHE_HOME/wa/media/avatars/sha256/ — shared across
	// profiles (public-facing bytes, sha256 keyed). Failure leaves
	// profile.setName/setStatus/avatar answering method_not_found.
	profileAdapter, prErr := waAdapter.NewProfileFor(resolver.AvatarRoot())
	if prErr != nil {
		log.Warn("profile adapter unavailable, profile.setName/setStatus/avatar disabled", "err", prErr)
		profileAdapter = nil
	}
	p.profile = profileAdapter

	// Step 7h (feature 018 T2-15..T2-18): construct GroupAdmin. Failure
	// leaves group.create/leave/add/remove/promote/demote/edit/invite.*
	// answering method_not_found.
	groupAdminAdapter, gaErr := waAdapter.NewGroupAdminFor()
	if gaErr != nil {
		log.Warn("group admin adapter unavailable, group.* methods disabled", "err", gaErr)
		groupAdminAdapter = nil
	}
	p.groupAdmin = groupAdminAdapter

	// Step 7i (feature 018 T2-20): construct PollManager. The v2.0.0 adapter
	// is receive-only — every well-formed poll.vote returns -32000
	// upstream_error until whatsmeow lands an outbound Vote helper. Failure
	// here leaves poll.vote answering method_not_found.
	pollsAdapter, pmErr := waAdapter.NewPollManagerFor()
	if pmErr != nil {
		log.Warn("poll manager adapter unavailable, poll.vote disabled", "err", pmErr)
		pollsAdapter = nil
	}
	p.polls = pollsAdapter

	return p, nil
}

// closeBestEffort closes the optional contacts + events stores in error
// paths. Both may be nil (best-effort open); the concrete nil check must
// happen on the typed pointer, not on an interface wrapper — a typed-nil
// through `interface{ Close() error }` is a non-nil interface and would
// panic on dispatch.
func closeBestEffort(events *sqliteevents.Store, contacts *sqlitecontacts.Store) {
	if events != nil {
		_ = events.Close()
	}
	if contacts != nil {
		_ = contacts.Close()
	}
}

// runRetentionCleanup deletes messages older than the retention period
// on startup and hourly. Respects the isSyncing flag to avoid bulk
// deletes during active history sync. Feature 009 — FR-035.
func runRetentionCleanup(ctx context.Context, store *sqlitehistory.Store, adapter interface{ IsSyncing() bool }, retention time.Duration, log *slog.Logger) {
	cleanup := func() {
		if adapter.IsSyncing() {
			log.Debug("retention: skipping, history sync in progress")
			return
		}
		deleted, err := store.CleanupRetention(ctx, retention)
		if err != nil {
			log.Error("retention cleanup failed", "err", err)
			return
		}
		if deleted > 0 {
			log.Info("retention cleanup", "deleted", deleted)
		}
	}

	cleanup() // run on startup
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

// closeWithTimeout wraps a Close() call with a deadline. If the call
// exceeds the timeout, it is abandoned and an error is logged. This
// prevents a hung component from blocking the entire shutdown sequence.
// Feature 009 — FR-040.
func closeWithTimeout(log *slog.Logger, name string, c interface{ Close() error }, timeout time.Duration) {
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case err := <-done:
		if err != nil {
			log.Error("close failed", "component", name, "err", err)
		}
	case <-time.After(timeout):
		log.Error("close timed out", "component", name, "timeout", timeout)
	}
}

// parseLogLevel reads --log-level from os.Args or WA_LOG_LEVEL from env.
// Defaults to INFO.
func parseLogLevel() slog.Level {
	raw := os.Getenv("WA_LOG_LEVEL")

	// Simple flag parsing for --log-level (no full flag library needed for
	// the daemon since it has only this one flag).
	for i, arg := range os.Args[1:] {
		if arg == "--log-level" && i+1 < len(os.Args)-1 {
			raw = os.Args[i+2]
			break
		}
		if after, ok := strings.CutPrefix(arg, "--log-level="); ok {
			raw = after
			break
		}
	}

	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

// webhookStoreOrNil flattens the typed-nil pointer into a nil
// interface so dispatcher handlers' nil-guards fire correctly.
func webhookStoreOrNil(s *sqlitewebhooks.Store) app.WebhookStore {
	if s == nil {
		return nil
	}
	return s
}
