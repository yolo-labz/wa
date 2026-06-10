package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitedrafts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteschedule"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitewebhooks"
)

// startupStores holds every per-profile SQLite store the daemon opens
// at boot, plus a startupCleanup primed for incremental error-path
// teardown. Spec 016 M-023 / T078 — extracts steps 1-3b of run().
//
// SessionStore + HistoryStore are NOT yet owned by waAdapter at this
// point: the caller (run()) flips Cleanup.adapterOwnsStores=true after
// wmAdapter.Open succeeds. Until then, an error path that reaches
// Cleanup.run() must close them itself, which startupCleanup already
// does via the !adapterOwnsStores branch.
//
// ContactsStore / EventsStore are best-effort: a nil value means
// "feature unavailable, dispatcher handlers degrade gracefully" — the
// daemon does NOT abort on either.
type startupStores struct {
	Cleanup       *startupCleanup
	SessionStore  *sqlitestore.Store
	HistoryStore  *sqlitehistory.Store
	DraftStore    *sqlitedrafts.Store
	ScheduleStore *sqliteschedule.Store
	ContactsStore *sqlitecontacts.Store
	WebhookStore  *sqlitewebhooks.Store
	EventsStore   *sqliteevents.Store
}

// openStores runs steps 1-3b of run(): ensures the per-profile XDG
// directories exist, then opens each SQLite store in fail-fast order.
// On any fatal failure the partially-open stores are torn down via
// startupCleanup before the error returns.
//
// Best-effort stores (contacts, events) are returned nil on open
// failure with a Warn log — the daemon continues.
func openStores(ctx context.Context, resolver *PathResolver, log *slog.Logger) (*startupStores, error) {
	if err := ensureDirs(resolver); err != nil {
		return nil, fmt.Errorf("ensureDirs: %w", err)
	}

	cleanup := &startupCleanup{log: log, shutdownTimeout: 5 * time.Second}
	s := &startupStores{Cleanup: cleanup}

	// Step 2: open sqlitestore (per-profile session.db).
	sessionDBPath := resolver.SessionDB()
	log.Info("opening session store", "path", sessionDBPath)
	sessionStore, err := sqlitestore.Open(ctx, sessionDBPath, wmAdapter.NewSlogLogger(log))
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: %w", err)
	}
	s.SessionStore = sessionStore
	cleanup.sessionStore = sessionStore

	// Step 3: open sqlitehistory (per-profile messages.db).
	historyDBPath := resolver.HistoryDB()
	backupsDir := resolver.BackupsDir()
	log.Info("opening history store", "path", historyDBPath, "backups", backupsDir)
	historyStore, err := sqlitehistory.OpenWithBackups(ctx, historyDBPath, backupsDir)
	if err != nil {
		cleanup.run()
		return nil, fmt.Errorf("sqlitehistory: %w", err)
	}
	s.HistoryStore = historyStore
	cleanup.historyStore = historyStore

	// Step 3a (feature 017): drafts.db + scheduled.db are fatal — half-
	// wired dispatcher would silently drop schedule.send / draft.approve.
	draftsDBPath := resolver.DraftsDB()
	log.Info("opening drafts store", "path", draftsDBPath)
	draftStore, err := sqlitedrafts.Open(ctx, draftsDBPath)
	if err != nil {
		cleanup.run()
		return nil, fmt.Errorf("sqlitedrafts: %w", err)
	}
	s.DraftStore = draftStore
	cleanup.draftStore = draftStore

	scheduleDBPath := resolver.ScheduleDB()
	log.Info("opening schedule store", "path", scheduleDBPath)
	scheduleStore, err := sqliteschedule.Open(ctx, scheduleDBPath)
	if err != nil {
		cleanup.run()
		return nil, fmt.Errorf("sqliteschedule: %w", err)
	}
	s.ScheduleStore = scheduleStore
	cleanup.scheduleStore = scheduleStore

	// Step 3b (feature 017): contacts.db + events.db are best-effort.
	// Failure degrades to nil; dispatcher handlers treat nil as
	// "feature unavailable" rather than erroring the daemon.
	webhooksDBPath := resolver.WebhooksDB()
	log.Info("opening webhooks store", "path", webhooksDBPath)
	webhookStore, wErr := sqlitewebhooks.Open(ctx, webhooksDBPath)
	if wErr != nil {
		log.Warn("webhooks store unavailable, continuing without", "err", wErr)
		webhookStore = nil
	}
	s.WebhookStore = webhookStore
	cleanup.webhookStore = webhookStore

	contactsDBPath := resolver.ContactsDB()
	log.Info("opening contacts store", "path", contactsDBPath)
	contactsStore, cErr := sqlitecontacts.Open(ctx, contactsDBPath)
	if cErr != nil {
		log.Warn("contacts store unavailable, continuing without", "err", cErr)
		contactsStore = nil
	}
	s.ContactsStore = contactsStore
	cleanup.contactsStore = contactsStore

	eventsDBPath := resolver.EventsDB()
	log.Info("opening events store", "path", eventsDBPath)
	eventsStore, eErr := sqliteevents.Open(ctx, eventsDBPath, 10000)
	if eErr != nil {
		log.Warn("events store unavailable, continuing without", "err", eErr)
		eventsStore = nil
	}
	s.EventsStore = eventsStore
	cleanup.eventsStore = eventsStore

	return s, nil
}
