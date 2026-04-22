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

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitedrafts"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqliteschedule"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitestore"
	wmAdapter "github.com/yolo-labz/wa/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
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

func main() {
	// Service management subcommands (install-service, uninstall-service)
	// are handled before the daemon starts.
	if handleServiceCommand() {
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wad: %v\n", err)
		os.Exit(1)
	}
}

//nolint:gocyclo // composition root is inherently sequential; splitting hurts readability
func run() error {
	// T017: parse --log-level / WA_LOG_LEVEL.
	level := parseLogLevel()
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	// Feature 009 — FR-037: set GOMEMLIMIT to prevent OOM from
	// malformed protobuf blobs. Default 512 MiB.
	debug.SetMemoryLimit(512 * 1024 * 1024)

	log.Info("wad starting")

	// Feature 008: resolve the active profile. This CLI parsing is
	// intentionally minimal (--profile flag, WA_PROFILE env, fallback to
	// "default") because wad is a daemon, not a CLI; the full precedence
	// chain (FR-001) lives in cmd/wa/profile.go.
	profile := resolveDaemonProfile()
	resolver, err := NewPathResolver(profile)
	if err != nil {
		return fmt.Errorf("profile %q: %w", profile, err)
	}
	log.Info("wad profile resolved", "profile", resolver.Profile())

	// Feature 008: detect and perform legacy-layout migration BEFORE any
	// adapter construction. See contracts/migration.md §When the migration
	// runs and FR-015..FR-022.
	if err := autoMigrate(resolver, log); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Step 1: create per-profile XDG directories.
	if err := ensureDirs(resolver); err != nil {
		return fmt.Errorf("ensureDirs: %w", err)
	}

	// Step 2: open sqlitestore (per-profile session.db).
	sessionDBPath := resolver.SessionDB()
	log.Info("opening session store", "path", sessionDBPath)
	sessionStore, err := sqlitestore.Open(context.Background(), sessionDBPath, wmAdapter.NewSlogLogger(log))
	if err != nil {
		return fmt.Errorf("sqlitestore: %w", err)
	}

	// Step 3: open sqlitehistory (per-profile messages.db).
	historyDBPath := resolver.HistoryDB()
	backupsDir := resolver.BackupsDir()
	log.Info("opening history store", "path", historyDBPath, "backups", backupsDir)
	historyStore, err := sqlitehistory.OpenWithBackups(context.Background(), historyDBPath, backupsDir)
	if err != nil {
		_ = sessionStore.Close()
		return fmt.Errorf("sqlitehistory: %w", err)
	}

	// Step 3a (feature 017): open per-profile drafts.db + scheduled.db.
	// These carry scheduled-send and human-review-draft state that must
	// survive restarts. Failure here is fatal — half-wired dispatcher
	// would silently drop schedule.send / draft.approve calls.
	draftsDBPath := resolver.DraftsDB()
	log.Info("opening drafts store", "path", draftsDBPath)
	draftStore, err := sqlitedrafts.Open(context.Background(), draftsDBPath)
	if err != nil {
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("sqlitedrafts: %w", err)
	}

	scheduleDBPath := resolver.ScheduleDB()
	log.Info("opening schedule store", "path", scheduleDBPath)
	scheduleStore, err := sqliteschedule.Open(context.Background(), scheduleDBPath)
	if err != nil {
		_ = draftStore.Close()
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("sqliteschedule: %w", err)
	}

	// Step 3b (feature 017): open per-profile contacts.db + events.db.
	// Best-effort: failure degrades to nil; dispatcher handlers treat
	// nil stores as "feature unavailable" rather than erroring the daemon.
	contactsDBPath := resolver.ContactsDB()
	log.Info("opening contacts store", "path", contactsDBPath)
	contactsStore, cErr := sqlitecontacts.Open(context.Background(), contactsDBPath)
	if cErr != nil {
		log.Warn("contacts store unavailable, continuing without", "err", cErr)
		contactsStore = nil
	}

	eventsDBPath := resolver.EventsDB()
	log.Info("opening events store", "path", eventsDBPath)
	eventsStore, eErr := sqliteevents.Open(context.Background(), eventsDBPath, 10000)
	if eErr != nil {
		log.Warn("events store unavailable, continuing without", "err", eErr)
		eventsStore = nil
	}

	// Step 4: open slogaudit (per-profile audit.log).
	auditLogPath := resolver.AuditLog()
	log.Info("opening audit log", "path", auditLogPath)
	auditLog, err := slogaudit.Open(auditLogPath)
	if err != nil {
		closeBestEffort(eventsStore, contactsStore)
		_ = scheduleStore.Close()
		_ = draftStore.Close()
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("slogaudit: %w", err)
	}

	// Step 5: load per-profile allowlist from allowlist.toml (or empty).
	allowlistPath := resolver.AllowlistTOML()
	log.Info("loading allowlist", "path", allowlistPath)
	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		_ = auditLog.Close()
		closeBestEffort(eventsStore, contactsStore)
		_ = scheduleStore.Close()
		_ = draftStore.Close()
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("allowlist: %w", err)
	}

	// Step 5a (feature 017 / T3-01): load per-profile config.toml. Missing
	// file → DefaultFeatureFlags.
	configPath := resolver.ConfigTOML()
	log.Info("loading config", "path", configPath)
	cfg, err := loadConfig(configPath)
	if err != nil {
		_ = auditLog.Close()
		closeBestEffort(eventsStore, contactsStore)
		_ = scheduleStore.Close()
		_ = draftStore.Close()
		_ = historyStore.Close()
		_ = sessionStore.Close()
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

	// Step 7: open whatsmeow adapter.
	log.Info("opening whatsmeow adapter")
	waAdapter, err := wmAdapter.Open(context.Background(), sessionStore, historyStore, allowlist, log)
	if err != nil {
		watchCancel()
		<-watchDone
		_ = auditLog.Close()
		closeBestEffort(eventsStore, contactsStore)
		_ = scheduleStore.Close()
		_ = draftStore.Close()
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("whatsmeow: %w", err)
	}

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

	// Step 7a (feature 017): construct media + labels adapters.
	// MediaAdapter holds the shared content-addressed cache root; it is
	// safe to proceed if the root mkdir fails — handlers surface
	// -32000 on first use rather than crashing the daemon.
	mediaAdapter, mErr := waAdapter.NewMediaAdapter(resolver.MediaRoot())
	if mErr != nil {
		log.Warn("media adapter unavailable, media.* methods will error", "err", mErr)
		mediaAdapter = nil
	}
	labelsAdapter := waAdapter.NewLabelsAdapter(nil)

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

	// Step 7c (feature 018 T2-07): construct ChatStateManager. Failure
	// leaves chat.archive/mute/pin/markUnread answering method_not_found.
	chatStateAdapter, csErr := waAdapter.NewChatStateFor()
	if csErr != nil {
		log.Warn("chat state adapter unavailable, chat.* methods disabled", "err", csErr)
		chatStateAdapter = nil
	}

	// Step 7d (feature 018 T2-09): construct Blocker. Failure leaves
	// contact.block/unblock/blocklist at method_not_found and the pre-send
	// gate in handleSend/handleSendMedia/handleReact/handleSendReply
	// no-ops (ensureNotBlocked returns nil when Blocker is nil).
	blockerAdapter, bkErr := waAdapter.NewBlockerFor()
	if bkErr != nil {
		log.Warn("blocker adapter unavailable, contact.block/unblock/blocklist disabled", "err", bkErr)
		blockerAdapter = nil
	}

	// Step 7e (feature 018 T2-11): construct PrivacySettings. Failure
	// leaves privacy.set/privacy.get answering method_not_found.
	privacyAdapter, pvErr := waAdapter.NewPrivacyFor()
	if pvErr != nil {
		log.Warn("privacy adapter unavailable, privacy.set/get disabled", "err", pvErr)
		privacyAdapter = nil
	}

	// Step 7f (feature 018 T2-12): construct SessionTerminator. The
	// LogoutAll helper is absent at the pinned whatsmeow commit, so the
	// adapter returns -32000 upstream_error until the upstream ships it.
	logoutAllAdapter, loErr := waAdapter.NewLogoutAllFor()
	if loErr != nil {
		log.Warn("logout-all adapter unavailable, session.logoutAll disabled", "err", loErr)
		logoutAllAdapter = nil
	}

	// Step 7g (feature 018 T2-13): construct ProfileEditor. Avatar cache
	// lives under $XDG_CACHE_HOME/wa/media/avatars/sha256/ — shared across
	// profiles (public-facing bytes, sha256 keyed). Failure leaves
	// profile.setName/setStatus/avatar answering method_not_found.
	profileAdapter, prErr := waAdapter.NewProfileFor(resolver.AvatarRoot())
	if prErr != nil {
		log.Warn("profile adapter unavailable, profile.setName/setStatus/avatar disabled", "err", prErr)
		profileAdapter = nil
	}

	// Step 7h (feature 018 T2-15..T2-18): construct GroupAdmin. Failure
	// leaves group.create/leave/add/remove/promote/demote/edit/invite.*
	// answering method_not_found.
	groupAdminAdapter, gaErr := waAdapter.NewGroupAdminFor()
	if gaErr != nil {
		log.Warn("group admin adapter unavailable, group.* methods disabled", "err", gaErr)
		groupAdminAdapter = nil
	}

	// Step 7i (feature 018 T2-20): construct PollManager. The v2.0.0 adapter
	// is receive-only — every well-formed poll.vote returns -32000
	// upstream_error until whatsmeow lands an outbound Vote helper. Failure
	// here leaves poll.vote answering method_not_found.
	pollsAdapter, pmErr := waAdapter.NewPollManagerFor()
	if pmErr != nil {
		log.Warn("poll manager adapter unavailable, poll.vote disabled", "err", pmErr)
		pollsAdapter = nil
	}

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

	dispatcher := app.NewDispatcher(app.DispatcherConfig{
		Sender:            waAdapter,
		Events:            waAdapter,
		Contacts:          waAdapter,
		Groups:            waAdapter,
		Session:           waAdapter,
		Allowlist:         allowlist,
		Audit:             auditLog,
		History:           waAdapter,
		Pairer:            waAdapter,
		Drafts:            draftStore,
		Media:             mediaAdapter,
		Scheduled:         scheduleStore,
		ScheduleRunner:    scheduleRunner,
		Labels:            labelsAdapter,
		Moderator:         moderatorPort(moderatorAdapter),
		ChatState:         chatStatePort(chatStateAdapter),
		Blocker:           blockerPort(blockerAdapter),
		Privacy:           privacyPort(privacyAdapter),
		SessionTerm:       logoutAllPort(logoutAllAdapter),
		ProfileEditor:     profileEditorPort(profileAdapter),
		GroupAdmin:        groupAdminPort(groupAdminAdapter),
		Polls:             pollsPort(pollsAdapter),
		IsBusinessAccount: isBusiness,
		Idempotency:       historyStore.IdempotencySidecar(),
		Features:          cfg.Features,
		Profile:           profile,
		SessionCreated:    sessionCreatedAt,
		Logger:            log,
		Safety:            safety,
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

	// Step 9: wire composition-root-level handlers for "allow" and "panic".
	// These methods need filesystem I/O and adapter access that the app
	// dispatcher cannot have, so they are intercepted before delegation.
	allowHandler := handleAllow(allowlist, &allowlistMu, allowlistPath, auditLog, log)
	panicHandler := handlePanic(waAdapter, waAdapter, auditLog, log)
	configFeaturesHandler := handleConfigFeatures(cfg.Features)

	// Step 10: construct dispatcherAdapter (app.Event -> socket.Event bridge).
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	da := newDispatcherAdapter(bridgeCtx, dispatcher, map[string]compositionHandler{
		"allow":           allowHandler,
		"panic":           panicHandler,
		"config.features": configFeaturesHandler,
	})

	// Step 11: construct socket.Server.
	server := socket.NewServer(da, log, socket.WithServerVersion(version))

	// Step 12: signal.NotifyContext for root context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Step 12-pre (feature 017): arm schedule-runner timers for any
	// pending rows persisted from a prior daemon run. Start MUST use the
	// daemon-lifetime ctx — timer callbacks capture it for fire dispatch.
	if cfg.Features.ScheduledSends {
		if err := scheduleRunner.Start(ctx); err != nil {
			log.Error("schedule runner start failed, scheduled sends disabled", "err", err)
		} else {
			log.Info("schedule runner started", "pending", scheduleRunner.Pending())
		}
	}

	// Resolve per-profile socket path.
	sockPath, err := resolver.SocketPath()
	if err != nil {
		scheduleRunner.Stop()
		bridgeCancel()
		da.Close()
		_ = dispatcher.Close()
		_ = waAdapter.Close()
		watchCancel()
		<-watchDone
		_ = auditLog.Close()
		closeBestEffort(eventsStore, contactsStore)
		_ = scheduleStore.Close()
		_ = draftStore.Close()
		return fmt.Errorf("socket path: %w", err)
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

	// Step 14: shutdown in reverse order per FR-033/FR-040.
	// Each Close() gets a 5-second timeout per FR-040.
	const shutdownTimeout = 5 * time.Second
	log.Info("shutdown: stopping socket server")

	log.Info("shutdown: stopping schedule runner")
	scheduleRunner.Stop()

	log.Info("shutdown: closing dispatcher adapter")
	bridgeCancel()
	da.Close()

	log.Info("shutdown: closing app dispatcher")
	closeWithTimeout(log, "app dispatcher", dispatcher, shutdownTimeout)

	log.Info("shutdown: closing whatsmeow adapter")
	closeWithTimeout(log, "whatsmeow adapter", waAdapter, shutdownTimeout)

	log.Info("shutdown: closing allowlist watcher")
	watchCancel()
	<-watchDone

	log.Info("shutdown: closing feature-017 stores")
	closeWithTimeout(log, "schedule store", scheduleStore, shutdownTimeout)
	closeWithTimeout(log, "drafts store", draftStore, shutdownTimeout)
	if contactsStore != nil {
		closeWithTimeout(log, "contacts store", contactsStore, shutdownTimeout)
	}
	if eventsStore != nil {
		closeWithTimeout(log, "events store", eventsStore, shutdownTimeout)
	}

	log.Info("shutdown: closing audit log")
	closeWithTimeout(log, "audit log", auditLog, shutdownTimeout)

	// Note: historyStore and sessionStore are closed by waAdapter.Close()
	// (the whatsmeow adapter owns their lifecycle per adapter.go:Close).

	log.Info("shutdown complete")
	return serverErr
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
