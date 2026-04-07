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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitestore"
	wmAdapter "github.com/yolo-labz/wa/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/internal/app"
)

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

func run() error {
	// T017: parse --log-level / WA_LOG_LEVEL.
	level := parseLogLevel()
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

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
	log.Info("opening history store", "path", historyDBPath)
	historyStore, err := sqlitehistory.Open(context.Background(), historyDBPath)
	if err != nil {
		_ = sessionStore.Close()
		return fmt.Errorf("sqlitehistory: %w", err)
	}

	// Step 4: open slogaudit (per-profile audit.log).
	auditLogPath := resolver.AuditLog()
	log.Info("opening audit log", "path", auditLogPath)
	auditLog, err := slogaudit.Open(auditLogPath)
	if err != nil {
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
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("allowlist: %w", err)
	}

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
		_ = historyStore.Close()
		_ = sessionStore.Close()
		return fmt.Errorf("whatsmeow: %w", err)
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
	if existing, loadErr := waAdapter.Load(context.Background()); loadErr == nil && !existing.CreatedAt().IsZero() {
		sessionCreatedAt = existing.CreatedAt()
		log.Info("sourced SessionCreated from session store", "ts", sessionCreatedAt)
	} else {
		log.Info("session not yet paired, SessionCreated defaults to now", "ts", sessionCreatedAt)
	}
	dispatcher := app.NewDispatcher(app.DispatcherConfig{
		Sender:         waAdapter,
		Events:         waAdapter,
		Contacts:       waAdapter,
		Groups:         waAdapter,
		Session:        waAdapter,
		Allowlist:      allowlist,
		Audit:          auditLog,
		History:        waAdapter,
		Pairer:         waAdapter,
		SessionCreated: sessionCreatedAt,
		Logger:         log,
	})

	// Step 9: wire composition-root-level handlers for "allow" and "panic".
	// These methods need filesystem I/O and adapter access that the app
	// dispatcher cannot have, so they are intercepted before delegation.
	allowHandler := handleAllow(allowlist, &allowlistMu, allowlistPath, auditLog, log)
	panicHandler := handlePanic(waAdapter, waAdapter, auditLog, log)

	// Step 10: construct dispatcherAdapter (app.Event -> socket.Event bridge).
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	da := newDispatcherAdapter(bridgeCtx, dispatcher, map[string]compositionHandler{
		"allow": allowHandler,
		"panic": panicHandler,
	})

	// Step 11: construct socket.Server.
	server := socket.NewServer(da, log)

	// Step 12: signal.NotifyContext for root context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Resolve per-profile socket path.
	sockPath, err := resolver.SocketPath()
	if err != nil {
		bridgeCancel()
		da.Close()
		_ = dispatcher.Close()
		_ = waAdapter.Close()
		watchCancel()
		<-watchDone
		_ = auditLog.Close()
		return fmt.Errorf("socket path: %w", err)
	}

	// Step 13: server.Run blocks until signal.
	log.Info("starting socket server", "path", sockPath)
	serverErr := server.Run(ctx, sockPath)

	// Step 14: shutdown in reverse order per FR-033.
	log.Info("shutdown: stopping socket server")
	// server.Run already returned, so the socket is closed.

	log.Info("shutdown: closing dispatcher adapter")
	bridgeCancel()
	da.Close()

	log.Info("shutdown: closing app dispatcher")
	_ = dispatcher.Close()

	log.Info("shutdown: closing whatsmeow adapter")
	_ = waAdapter.Close()

	log.Info("shutdown: closing allowlist watcher")
	watchCancel()
	<-watchDone

	log.Info("shutdown: closing audit log")
	_ = auditLog.Close()

	// Note: historyStore and sessionStore are closed by waAdapter.Close()
	// (the whatsmeow adapter owns their lifecycle per adapter.go:Close).

	log.Info("shutdown complete")
	return serverErr
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
		if strings.HasPrefix(arg, "--log-level=") {
			raw = strings.TrimPrefix(arg, "--log-level=")
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
