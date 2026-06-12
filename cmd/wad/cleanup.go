package main

import (
	"context"
	"log/slog"
	"time"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitedrafts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteschedule"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitewebhooks"
)

// startupCleanup centralises the reverse-order teardown that ran inline
// at every early-return in run(). Spec 016 H-013 / T020.
//
// Each field is set by run() as the corresponding resource opens
// successfully. A failure in a later step calls (*startupCleanup).run()
// to close everything that already opened, in the same reverse order
// the success-path shutdown uses. Nil fields are skipped, so
// startupCleanup is safe to call mid-startup before every store has
// been wired.
//
// One subtle invariant: the whatsmeow adapter takes ownership of
// historyStore + sessionStore via waAdapter.Close (see
// adapter.go:Close). After wmAdapter.Open succeeds we MUST NOT close
// those stores ourselves on the error path — Adapter.Close will. The
// `adapterOwnsStores` field guards this.
type startupCleanup struct {
	log *slog.Logger

	// daemonCancel aborts the daemon-lifetime context captured by
	// long-lived callbacks (CON-01). Fired FIRST in both teardown
	// paths so in-flight queries unwind before their stores close.
	daemonCancel context.CancelFunc
	// retentionDone closes when the retention-cleanup goroutine has
	// exited (CON-06). Joined before the dispatch chain closes the
	// history store the goroutine queries.
	retentionDone chan struct{}

	// Synchronous teardown timers + best-effort handles. Set as the
	// corresponding step succeeds in run(); nil otherwise.
	shutdownTimeout time.Duration
	healthHTTP      *healthHTTPServer
	restShutdown    func(context.Context) error
	scheduleRunner  *app.ScheduleRunner

	bridgeCancel context.CancelFunc
	da           *dispatcherAdapter
	dispatcher   *app.Dispatcher
	waAdapter    *wmAdapter.Adapter

	watchCancel context.CancelFunc
	watchDone   chan struct{}

	auditLog *slogaudit.Audit
	// eventsPumpStop joins the ARCH-01 events pump goroutine. MUST run
	// before eventsStore closes — the pump appends to that db.
	eventsPumpStop    func()
	eventsStore       *sqliteevents.Store
	contactsStore     *sqlitecontacts.Store
	scheduleStore     *sqliteschedule.Store
	webhookStore      *sqlitewebhooks.Store
	webhookWorker     *app.WebhookWorker
	draftStore        *sqlitedrafts.Store
	historyStore      *sqlitehistory.Store
	sessionStore      *sqlitestore.Store
	adapterOwnsStores bool
}

// run tears down every set field, in the reverse order success-path
// shutdown uses. Safe to call multiple times — every Close call is
// guarded against the field being nil. Errors from individual Close
// calls are logged but do not short-circuit (graceful-shutdown
// semantics, FR-040).
func (c *startupCleanup) run() {
	if c.daemonCancel != nil {
		c.daemonCancel()
	}
	c.runHTTPShutdown()
	c.waitRetention()
	c.runDispatchShutdown()
	c.runWatcherShutdown()
	c.runStoresShutdown()
}

// runHTTPShutdown stops the schedule runner + REST + health listeners.
// Each Shutdown gets a private timeout context so a hung listener
// cannot block the rest of the cleanup chain.
func (c *startupCleanup) runHTTPShutdown() {
	if c.scheduleRunner != nil {
		c.scheduleRunner.Stop()
	}
	if c.restShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
		if err := c.restShutdown(ctx); err != nil && c.log != nil {
			c.log.Error("startup-cleanup rest shutdown", "err", err)
		}
		cancel()
	}
	if c.healthHTTP != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
		if err := c.healthHTTP.Shutdown(ctx); err != nil && c.log != nil {
			c.log.Error("startup-cleanup health shutdown", "err", err)
		}
		cancel()
	}
}

// runDispatchShutdown closes the dispatcher-bridge ⇨ dispatcher ⇨
// adapter chain.
func (c *startupCleanup) runDispatchShutdown() {
	if c.bridgeCancel != nil {
		c.bridgeCancel()
	}
	if c.da != nil {
		c.da.Close()
	}
	if c.dispatcher != nil {
		c.closeLogged("app dispatcher", c.dispatcher)
	}
	if c.waAdapter != nil {
		c.closeLogged("whatsmeow adapter", c.waAdapter)
	}
}

// closeLogged closes cl and logs any error. The startup-error teardown
// has no caller to propagate to, so this log line is the only surface a
// failed WAL checkpoint or audit flush gets (SF-02, 018 audit).
func (c *startupCleanup) closeLogged(name string, cl interface{ Close() error }) {
	if err := cl.Close(); err != nil && c.log != nil {
		c.log.Error("startup-cleanup close failed", "component", name, "err", err)
	}
}

// runWatcherShutdown stops the allowlist watcher goroutine and waits
// for it to exit.
func (c *startupCleanup) runWatcherShutdown() {
	if c.watchCancel != nil {
		c.watchCancel()
	}
	if c.watchDone != nil {
		<-c.watchDone
	}
}

// runStoresShutdown closes the audit log and the per-feature SQLite
// stores. historyStore + sessionStore are skipped when waAdapter owns
// them (post-Open success).
func (c *startupCleanup) runStoresShutdown() {
	if c.auditLog != nil {
		c.closeLogged("audit log", c.auditLog)
	}
	if c.eventsPumpStop != nil {
		c.eventsPumpStop()
	}
	if c.eventsStore != nil {
		c.closeLogged("events store", c.eventsStore)
	}
	if c.contactsStore != nil {
		c.closeLogged("contacts store", c.contactsStore)
	}
	if c.webhookStore != nil {
		c.closeLogged("webhooks store", c.webhookStore)
	}
	if c.scheduleStore != nil {
		c.closeLogged("schedule store", c.scheduleStore)
	}
	if c.draftStore != nil {
		c.closeLogged("drafts store", c.draftStore)
	}
	if c.adapterOwnsStores {
		return
	}
	if c.historyStore != nil {
		c.closeLogged("history store", c.historyStore)
	}
	if c.sessionStore != nil {
		c.closeLogged("session store", c.sessionStore)
	}
}

// gracefulShutdown runs the success-path Step 14 sequence: stop
// scheduleRunner / REST / health, drain dispatcher chain, stop
// allowlist watcher, close every store. Each Close goes through
// closeWithTimeout per FR-040 so a hung component cannot block the
// whole sequence. Spec 016 M-023 / T080.
//
// historyStore + sessionStore are skipped because adapterOwnsStores
// is always true at this point — Adapter.Close transitively closes
// both via internal/adapters/secondary/whatsmeow/adapter.go:Close.
func (c *startupCleanup) gracefulShutdown() {
	c.shutdownLog("shutdown: stopping socket server")
	if c.daemonCancel != nil {
		c.daemonCancel()
	}
	c.shutdownHTTP()
	c.waitRetention()
	c.shutdownDispatchChain()
	c.shutdownWatcher()
	c.shutdownStores()
	c.shutdownLog("shutdown complete")
}

// waitRetention joins the retention-cleanup goroutine (CON-06). The
// root signal ctx (success path) or daemonCancel (error path) is
// already cancelled when this runs, so the goroutine's select exits on
// its own; the join provides the happens-before with the history-store
// close that follows. Bounded by shutdownTimeout per FR-040 so one
// slow CleanupRetention query cannot wedge the whole sequence.
func (c *startupCleanup) waitRetention() {
	if c.retentionDone == nil {
		return
	}
	select {
	case <-c.retentionDone:
	case <-time.After(c.shutdownTimeout):
		if c.log != nil {
			c.log.Error("shutdown: retention cleanup did not exit within timeout", "timeout", c.shutdownTimeout)
		}
	}
}

func (c *startupCleanup) shutdownHTTP() {
	c.shutdownLog("shutdown: stopping schedule runner")
	if c.scheduleRunner != nil {
		c.scheduleRunner.Stop()
	}
	if c.restShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
		if err := c.restShutdown(ctx); err != nil && c.log != nil {
			c.log.Error("shutdown rest http", "err", err)
		}
		cancel()
	}
	if c.healthHTTP != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
		if err := c.healthHTTP.Shutdown(ctx); err != nil && c.log != nil {
			c.log.Error("shutdown health http", "err", err)
		}
		cancel()
	}
}

func (c *startupCleanup) shutdownDispatchChain() {
	c.shutdownLog("shutdown: closing dispatcher adapter")
	if c.bridgeCancel != nil {
		c.bridgeCancel()
	}
	if c.da != nil {
		c.da.Close()
	}
	c.shutdownLog("shutdown: closing app dispatcher")
	if c.dispatcher != nil {
		closeWithTimeout(c.log, "app dispatcher", c.dispatcher, c.shutdownTimeout)
	}
	c.shutdownLog("shutdown: closing whatsmeow adapter")
	if c.waAdapter != nil {
		closeWithTimeout(c.log, "whatsmeow adapter", c.waAdapter, c.shutdownTimeout)
	}
}

func (c *startupCleanup) shutdownWatcher() {
	c.shutdownLog("shutdown: closing allowlist watcher")
	if c.watchCancel != nil {
		c.watchCancel()
	}
	if c.watchDone != nil {
		<-c.watchDone
	}
}

func (c *startupCleanup) shutdownStores() {
	c.shutdownLog("shutdown: closing feature-017 stores")
	if c.webhookWorker != nil {
		c.webhookWorker.Stop()
	}
	if c.webhookStore != nil {
		closeWithTimeout(c.log, "webhooks store", c.webhookStore, c.shutdownTimeout)
	}
	if c.scheduleStore != nil {
		closeWithTimeout(c.log, "schedule store", c.scheduleStore, c.shutdownTimeout)
	}
	if c.draftStore != nil {
		closeWithTimeout(c.log, "drafts store", c.draftStore, c.shutdownTimeout)
	}
	if c.contactsStore != nil {
		closeWithTimeout(c.log, "contacts store", c.contactsStore, c.shutdownTimeout)
	}
	if c.eventsPumpStop != nil {
		c.eventsPumpStop()
	}
	if c.eventsStore != nil {
		closeWithTimeout(c.log, "events store", c.eventsStore, c.shutdownTimeout)
	}
	c.shutdownLog("shutdown: closing audit log")
	if c.auditLog != nil {
		closeWithTimeout(c.log, "audit log", c.auditLog, c.shutdownTimeout)
	}
	// historyStore + sessionStore are owned by waAdapter (closed in
	// shutdownDispatchChain via Adapter.Close). Skip here.
}

// shutdownLog is the nil-safe Info shim used by gracefulShutdown so
// the trace stays readable without 11 inline `if c.log != nil`
// guards.
func (c *startupCleanup) shutdownLog(msg string) {
	if c.log != nil {
		c.log.Info(msg)
	}
}
