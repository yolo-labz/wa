// Package whatsmeow is the secondary adapter that wraps go.mau.fi/whatsmeow
// and translates between its types and the core domain types. Commit 4
// stitches the translators, flags, audit ring, fake client, and log bridge
// from commit 3 into a working Adapter that satisfies the eight secondary
// ports declared in internal/app (seven original ports plus HistoryStore
// added by feature 003 per Cockburn rule 20).
package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	waClient "go.mau.fi/whatsmeow"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// sessionContainer is the package-private interface commit 6's sqlitestore
// package will satisfy. It is intentionally minimal: the Adapter only needs
// to acquire a *sqlstore.Container (so it can pass a *store.Device to
// whatsmeow.NewClient) and to close the underlying SQLite handle on shutdown.
//
// Tests inject a stub that returns nil for Container — they never drive the
// Open() production path and instead use openWithClient() below.
type sessionContainer interface {
	// Container returns the whatsmeow sqlstore container. Production
	// returns a non-nil *sqlstore.Container; tests may return nil because
	// openWithClient skips this call path entirely.
	Container() *sqlstore.Container
	// Close releases the underlying SQLite handle.
	Close() error
}

// historyContainer is the package-private interface that the
// sqlitehistory package satisfies. It is the local-persistence layer
// consulted first by HistoryStore.LoadMore before any remote backfill.
// Feature 009 added InsertRaw for rich-metadata persistence from the
// whatsmeow event handler, and renamed the legacy path to
// InsertDomainMessages.
type historyContainer interface {
	LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
	InsertDomainMessages(ctx context.Context, msgs []domain.Message) error
	// InsertRaw has 10 params because it bridges two adapter packages that
	// deliberately do not share types (hexagonal boundary). SonarQube go:S107
	// is accepted here — the alternative (shared struct type) would couple
	// the two adapter packages or require a shared types package, violating
	// the hexagonal invariant.
	InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool, rawProto []byte, senderAltJID, addressingMode string) error
	// InsertRawInteractive is InsertRaw plus the JSON-encoded interactive
	// reply payload (issue #201). Only the inbound persist path needs it
	// (outbound sends + history-sync never carry a list/button selection),
	// so it is a separate method rather than a 14th positional InsertRaw
	// param — keeping the two no-interactive callers untouched and the
	// hexagonal boundary type-free (interactiveJSON crosses as bytes, not a
	// shared struct). Pass nil interactiveJSON to store SQL NULL.
	InsertRawInteractive(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool, rawProto []byte, senderAltJID, addressingMode string, interactiveJSON []byte) error
	GetRawProto(ctx context.Context, messageID string) (chatJID string, rawProto []byte, err error)
	GetSender(ctx context.Context, messageID string) (senderJID string, err error)
	Search(ctx context.Context, query string, limit int) ([]domain.Message, error)
	// PutReceipt persists a delivery/read receipt; GetThread reads a chat's
	// messages + receipts. Together they back the app.ThreadReader the
	// Adapter delegates to (spec-017 receipt path).
	PutReceipt(ctx context.Context, r domain.MessageReceipt) error
	GetThread(ctx context.Context, chat domain.JID, cursor app.ThreadCursor, limit int) (app.ThreadPage, error)
	Close() error
}

// Adapter is the whatsmeow secondary adapter. It is the single entry point
// for every outbound and inbound operation between the core and the
// whatsmeow library. It satisfies the eight secondary ports declared in
// internal/app (plus the test-only porttest.Adapter surface via the Seed*
// helpers used from the //go:build integration contract suite).
//
// Architecture note (016-code-quality-audit, C-002): the Adapter struct has
// 22 fields, but methods are already organized into focused files by
// responsibility (send.go, pair.go, history.go, history_sync.go, stream.go,
// groups.go, directory.go, session.go, markread.go, allowlist.go).
// Extracting sub-structs was evaluated and rejected because every candidate
// (historySyncWorker, auditWriter) requires 5+ back-references to shared
// fields (client, history, logger, nowFn, auditBuf), which adds indirection
// without reducing cognitive load.  This matches the Go community pattern:
// Three Dots Labs wild-workouts, Sam Smith's hexagonal guide, and the
// "Exploding Large Go Structs" article all recommend file-based separation
// over sub-struct extraction when shared state dominates.  See
// specs/016-code-quality-audit/research.md §1 for citations.
type Adapter struct {
	client    whatsmeowClient
	session   sessionContainer
	history   historyContainer
	allowlist *domain.Allowlist
	auditBuf  *auditRingBuffer
	logger    *slog.Logger
	// profile is the active wa profile, stamped on OTel spans opened
	// from adapter goroutines (history sync). Zero value is the
	// documented fallback — the span still fires, just with an empty
	// wa.profile attribute. Set via SetProfile from the composition
	// root.
	profile string

	// clientCtx is the detached context that governs the whatsmeow client
	// and all long-running goroutines. Per CLAUDE.md §"Daemon, IPC,
	// single-instance" and research §OPEN-Q8 the whatsmeow client lifetime
	// MUST NOT be tied to a request context — this context is only
	// cancelled from Close(), never from a per-call parent.
	clientCtx    context.Context
	clientCancel context.CancelFunc

	// eventCh is the bounded buffer between whatsmeow's push-based
	// AddEventHandlerWithSuccessStatus and the pull-based EventStream port.
	// Cap 256 is the research §D6 default: large enough to absorb a
	// reconnect burst, small enough to surface backpressure quickly.
	eventCh  chan domain.Event
	eventSeq atomic.Uint64

	// eventRing is the replay buffer consulted by ResumeFrom. Capacity
	// matches eventCh so a full buffer + full ring is the worst case.
	// Feature 018 T1-13 / R-04.
	eventRing *eventRingBuffer

	// resumeQueue buffers events to deliver on the next Next() calls
	// after ResumeFrom. resumeMu guards both fields. Protected by a
	// dedicated mutex (not overlayMu) because Next is on the hot path
	// and must not contend with seed-helper writers.
	resumeQueue []domain.Event
	resumeMu    sync.Mutex

	// panicArtefacts names per-profile filesystem paths that Panic
	// must remove on R-07 full-wipe. Set by SetPanicArtefacts from the
	// composition root after Open; empty fields are skipped.
	// panicDone flips to true exactly once per adapter lifetime so a
	// repeated Panic call is a safe no-op.
	panicMu        sync.Mutex
	panicArtefacts PanicArtefacts
	panicDone      bool

	// historyReqs is the per-request-ID routing table for on-demand
	// history sync responses (HS2). Entries are keyed by the request ID
	// returned by BuildHistorySyncRequest; the value is a channel the
	// caller of LoadMore blocks on. Every terminal path (success,
	// timeout, cancellation) deletes its entry — this is the
	// never-leak invariant from Clarifications round 2 Q1.
	historyReqs sync.Map

	// historySyncCh feeds the background history sync goroutine.
	// Feature 009 — FR-009, FR-026.
	historySyncCh chan any
	historySyncWg sync.WaitGroup
	isSyncing     atomic.Bool // true during active history sync processing

	// forceSyncTimeout overrides the per-round-trip deadline used by
	// ForceHistorySync (#174). Zero means "use historyRequestTimeout" —
	// the field exists so white-box tests can shrink the 30s default
	// without a real HistorySync round-trip. Production never sets it.
	forceSyncTimeout time.Duration

	// backfillLoginWait bounds how long BackfillRecent waits for the
	// post-reconnect login to settle before issuing its on-demand pull
	// (spec 110g backfill extension). Zero means backfillLoginWaitDefault.
	// White-box tests shrink it; production never sets it.
	backfillLoginWait time.Duration
	// backfillPollEvery is the login-state poll interval BackfillRecent
	// uses while waiting. Zero means backfillPollEveryDefault. Test-only
	// override, like backfillLoginWait.
	backfillPollEvery time.Duration

	// panicWg tracks the fire-and-forget goroutine that runs Panic on
	// events.LoggedOut. handleWAEvent dispatches into a goroutine because
	// SynchronousAck=true requires the handler to return promptly, but
	// Close() MUST wait for that goroutine before closing the SQLite
	// handles or it races with Panic's own container close. PR #136.
	panicWg sync.WaitGroup

	// closed flips to true exactly once from Close() to make it safe to
	// call repeatedly.
	closed atomic.Bool

	// wsConnected reflects the live websocket connection state. Spec 110g:
	// the cmd/wad soft-stale watchdog reads this via the WebsocketProbe
	// port to distinguish "no inbound traffic and websocket down" (real
	// disconnect, not stale) from "no inbound traffic but websocket up"
	// (soft stale — silent failure). Updated from handleWAEvent on every
	// ConnectionEvent translation.
	wsConnected atomic.Bool

	// pairSuccessCh is a buffered (cap 1) signal channel that the
	// phone-pairing-code branch of Pair() blocks on while waiting for the
	// upstream events.PairSuccess to arrive. handleWAEvent does a
	// non-blocking send into this channel when it sees a PairingEvent in
	// the PairSuccess state. Buffer 1 + non-blocking send means a
	// dropped signal is safe (there's only ever one pairing in flight).
	pairSuccessCh chan struct{}

	// nowFn is the clock used by handleWAEvent -> translateEvent. Tests
	// inject a deterministic clock; production uses time.Now.
	nowFn func() time.Time

	// pushNameSink, when non-nil, is called from persistInboundMessage with
	// the sender JID + pushName carried by the inbound *events.Message.
	// Wired at composition time by SetPushNameRefresher — the app layer
	// owns the "should I upsert" policy so the adapter stays free of
	// ContactSearcher knowledge (CLAUDE.md rule 23, FR-028). Nil by default
	// so unit tests and the pre-018 wiring continue to work.
	pushNameSink func(ctx context.Context, jid domain.JID, pushName string)

	// mediaResolver loads a content-addressed payload by sha256 so the
	// SHA256-source branch of buildMediaMessage (a remote client sending
	// `sendMedia --sha256 <hex>` against an already-uploaded object) can
	// read the bytes off the daemon's media store. Nil by default — when
	// unset the SHA256 branch returns domain.ErrMediaUnsupported, matching
	// the spec-197 seam's "fail loudly" contract. Wired by SetMediaResolver
	// from the composition root after the MediaAdapter is built. Spec 198.
	mediaResolver mediaResolver

	// --- porttest.Adapter test overlay ---
	// These maps exist so the //go:build integration contract suite can
	// seed deterministic state without reaching into whatsmeow internals.
	// Production code does not consult them: Lookup/Get/List/LoadMore fall
	// through to the overlay only when the underlying whatsmeow call
	// returns not-found or the overlay is non-empty. Guarded by overlayMu.
	overlayMu    sync.Mutex
	seedContacts map[domain.JID]domain.Contact
	seedGroups   map[domain.JID]domain.Group
	seedSession  domain.Session
	seedHistory  map[domain.JID][]domain.Message

	// testEvtMu guards testEvtQ and deliveredIDs — the porttest-only
	// overlay that supports the R-10 fake-driven contract suite. Keeping
	// them off eventCh preserves production bounded-send semantics while
	// still letting a test enqueue 1000 events in a row for ES6. Production
	// code never reads or writes these fields.
	testEvtMu     sync.Mutex
	testEvtQ      []domain.Event
	deliveredIDs  map[domain.EventID]struct{}
	deliveredList []domain.EventID
}

// deliveredIDsCap bounds the porttest-only Ack tracking set. Sized large
// enough for ES6 (1000 events) with headroom; FIFO eviction keeps memory
// flat across long-running test suites.
const deliveredIDsCap = 4096

// Open is the production constructor. It takes injected session and
// history stores (commits 6 and 7), an allowlist, and a slog logger,
// and returns an Adapter wired to a fresh *whatsmeow.Client.
//
// Construction order (data-model.md §"Construction order"):
//  1. Acquire a *store.Device from the session container.
//  2. Mutate DeviceProps.HistorySyncConfig to bound the history source
//     (FR-019: 7 days / 20 MiB / 100 MiB).
//  3. whatsmeow.NewClient(device, NewSlogLogger(logger)).
//  4. applyProductionFlags(client) — the 8 flags in flags.go.
//  5. Create the detached clientCtx (NOT parentCtx).
//  6. Allocate eventCh (cap 256) and auditBuf (cap 1000).
//  7. AddEventHandlerWithSuccessStatus(handleWAEvent).
//  8. Return the Adapter.
//
// Open does NOT call Connect(); the daemon's composition root in feature
// 004 decides when to connect (after the allowlist is loaded and the
// socket server is listening).
func Open(parentCtx context.Context, session sessionContainer, history historyContainer, allowlist *domain.Allowlist, logger *slog.Logger) (*Adapter, error) {
	if session == nil {
		return nil, errors.New("whatsmeow adapter: Open requires a non-nil sessionContainer")
	}
	if logger == nil {
		return nil, errors.New("whatsmeow adapter: Open requires a non-nil logger")
	}
	container := session.Container()
	if container == nil {
		return nil, errors.New("whatsmeow adapter: sessionContainer.Container() returned nil")
	}

	// Step 1: acquire a device. GetFirstDevice returns an existing
	// device if the store already has one, or a fresh unpaired device
	// otherwise. The "no device at all" error path is impossible with
	// the current sqlstore contract, but we guard against it anyway.
	device, err := container.GetFirstDevice(parentCtx)
	if err != nil {
		return nil, fmt.Errorf("whatsmeow adapter: GetFirstDevice: %w", err)
	}
	if device == nil {
		return nil, errors.New("whatsmeow adapter: GetFirstDevice returned nil device")
	}

	// Step 2: configure device identity + history sync bounds.
	// store.DeviceProps is a package-level var on go.mau.fi/whatsmeow/store
	// that whatsmeow reads during pairing. Mutating it here is the
	// sanctioned way to override the defaults before NewClient runs —
	// matches the approach used by mautrix/whatsapp.
	if store.DeviceProps == nil {
		return nil, errors.New("whatsmeow adapter: store.DeviceProps is nil — sqlstore schema drift?")
	}
	// Device identity: show "wa · yolo-labz" with the desktop monitor
	// icon in WhatsApp's Linked Devices screen. DESKTOP (7) renders
	// the computer monitor icon — the same one the real macOS WhatsApp
	// client uses. CATALINA (12) unexpectedly renders as "Portal TV"
	// with Meta's Portal icon. WhatsApp does not support custom icons.
	store.DeviceProps.Os = new("wa · yolo-labz")
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	store.DeviceProps.HistorySyncConfig = historySyncConfig()

	// Step 3: construct the whatsmeow client with the slog bridge.
	wmClient := waClient.NewClient(device, NewSlogLogger(logger))

	// Step 4: apply the 8 production flags in one place.
	applyProductionFlags(wmClient)

	// Step 5: detached context. This is the critical CLAUDE.md §"Daemon"
	// invariant: the whatsmeow client lifetime is NOT tied to parentCtx.
	clientCtx, clientCancel := context.WithCancel(context.Background())

	a := &Adapter{
		client:        &realClient{Client: wmClient},
		session:       session,
		history:       history,
		allowlist:     allowlist,
		auditBuf:      newAuditRing(1000),
		logger:        logger,
		clientCtx:     clientCtx,
		clientCancel:  clientCancel,
		eventCh:       make(chan domain.Event, 256),
		eventRing:     newEventRingBuffer(256),
		nowFn:         time.Now,
		seedContacts:  make(map[domain.JID]domain.Contact),
		seedGroups:    make(map[domain.JID]domain.Group),
		seedHistory:   make(map[domain.JID][]domain.Message),
		pairSuccessCh: make(chan struct{}, 1),
		historySyncCh: make(chan any, historySyncChCap),
		deliveredIDs:  make(map[domain.EventID]struct{}),
	}

	// Step 7a: start the background history sync worker (feature 009).
	a.historySyncWg.Add(1)
	go a.runHistorySyncWorker(clientCtx)

	// Step 7: register the event handler. SynchronousAck=true (flags.go)
	// means whatsmeow waits for handleWAEvent to return before acking
	// the upstream message, so a dropped handler cannot silently lose.
	a.client.AddEventHandlerWithSuccessStatus(a.handleWAEvent)

	// Step 8: if the loaded device is already paired, auto-connect the
	// websocket so the daemon is immediately functional on restart. If
	// not paired, the client stays idle until Pair() is called.
	if device.ID != nil {
		logger.Info("whatsmeow device already paired, auto-connecting", "jid", device.ID.String())
		if err := a.client.Connect(); err != nil {
			logger.Warn("whatsmeow auto-connect failed", "error", err)
			// Non-fatal — the daemon still starts; the operator can
			// inspect status and diagnose. Reconnect will be attempted
			// by whatsmeow's internal loop.
		}
	}

	return a, nil
}

// openWithClient is the package-private test constructor. It skips the
// whatsmeow.NewClient path entirely and wires a pre-built whatsmeowClient
// (typically a fakeWhatsmeowClient) directly. Used by unit tests and by
// openWithClient-shaped callers in //go:build integration tests that
// need to inject a mid-fidelity fake.
func openWithClient(client whatsmeowClient, allowlist *domain.Allowlist, logger *slog.Logger, nowFn func() time.Time) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if allowlist == nil {
		allowlist = domain.NewAllowlist()
	}
	clientCtx, clientCancel := context.WithCancel(context.Background())
	a := &Adapter{
		client:        client,
		allowlist:     allowlist,
		auditBuf:      newAuditRing(1000),
		logger:        logger,
		clientCtx:     clientCtx,
		clientCancel:  clientCancel,
		eventCh:       make(chan domain.Event, 256),
		eventRing:     newEventRingBuffer(256),
		nowFn:         nowFn,
		seedContacts:  make(map[domain.JID]domain.Contact),
		seedGroups:    make(map[domain.JID]domain.Group),
		seedHistory:   make(map[domain.JID][]domain.Message),
		pairSuccessCh: make(chan struct{}, 1),
		historySyncCh: make(chan any, historySyncChCap),
		deliveredIDs:  make(map[domain.EventID]struct{}),
	}
	a.historySyncWg.Add(1)
	go a.runHistorySyncWorker(clientCtx)
	a.client.AddEventHandlerWithSuccessStatus(a.handleWAEvent)
	return a
}

// SetProfile records the active wa profile on the adapter. Used by
// OTel spans opened from adapter-owned goroutines (history sync).
// Safe to call once at startup before any background worker runs;
// concurrent calls during Run are racy and forbidden.
func (a *Adapter) SetProfile(p string) { a.profile = p }

// SetMediaResolver wires the content-addressed store the SHA256-source
// branch of buildMediaMessage reads from (spec 198). Safe to call once at
// startup before any Send runs; concurrent calls during Run are racy and
// forbidden, mirroring SetProfile. Passing nil leaves the seam disabled
// (SHA256 sends return domain.ErrMediaUnsupported).
func (a *Adapter) SetMediaResolver(m mediaResolver) { a.mediaResolver = m }

// WebsocketConnected implements the app.WebsocketProbe port (spec 110g).
// Reports the last-known websocket state, flipped by handleWAEvent on
// ConnectionEvent translation. Used by the soft-stale watchdog and the
// health RPC to distinguish a hard disconnect from a silent stall.
func (a *Adapter) WebsocketConnected() bool { return a.wsConnected.Load() }

// Reconnect forces a fresh websocket handshake — Disconnect() then
// Connect(). It is the recovery action for the soft-stale path (spec
// 110g recover extension): a "zombie" link where whatsmeow's keepalive
// still answers PINGs but WhatsApp has silently demoted the device and
// stopped delivering inbound traffic, so WebsocketConnected() reports
// true while no events arrive. Disconnect()+Connect() re-runs the Noise
// handshake and re-auth, which the server treats as a fresh device
// session and resumes delivery.
//
// Safe by construction: it touches only the live socket, never
// session.db or the pairing, so no QR re-scan is required (same device).
// ctx is honoured for cancellation before the reconnect; whatsmeow's
// Connect has no context parameter of its own. A nil/no-op client (test
// composition without a wired client) makes this a no-op returning nil.
func (a *Adapter) Reconnect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.client == nil {
		return nil
	}
	a.client.Disconnect()
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("whatsmeow reconnect: %w", err)
	}
	return nil
}

// Close shuts the adapter down. It cancels clientCtx, disconnects the
// whatsmeow client, closes the history and session containers in order,
// and joins any errors per research §D8. Close is idempotent; subsequent
// calls return nil.
func (a *Adapter) Close() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	a.clientCancel()
	// Wait for the history sync worker to drain. clientCancel above
	// causes the select in runHistorySyncWorker to exit.
	a.historySyncWg.Wait()
	// Wait for any in-flight events.LoggedOut Panic goroutine to finish
	// before we close the SQLite containers — Panic also closes them
	// (panic.go step 4) and double-close races on file handles. PR #136.
	a.panicWg.Wait()
	if a.client != nil {
		a.client.Disconnect()
	}
	var errs []error
	if a.history != nil {
		if err := a.history.Close(); err != nil {
			errs = append(errs, fmt.Errorf("history close: %w", err))
		}
	}
	if a.session != nil {
		if err := a.session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("session close: %w", err))
		}
	}
	return errors.Join(errs...)
}

// handleWAEvent is the whatsmeow event dispatcher. It translates raw
// events via the pure translateEvent helper from commit 3 and enqueues
// the resulting domain.Event onto eventCh. It is invoked synchronously
// by whatsmeow (SynchronousAck=true) so the return value tells whatsmeow
// whether to ack the upstream message.
//
// Return semantics:
//   - true: the event was successfully handled (queued, ignored, or
//     routed to a side-effect handler). whatsmeow will ack upstream.
//   - false: the event could not be queued (buffer full, clientCtx
//     cancelled). whatsmeow will NOT ack; upstream will redeliver.
func (a *Adapter) handleWAEvent(rawEvt any) bool {
	// Spec 110g diagnostics: log the offline-message count delivered on
	// every (re)connect — the decisive signal for the soft-stale recover
	// path. count>0 ⇒ the recover reconnect FLUSHED queued inbound (the
	// link was a real zombie dropping messages, so auto-recover is
	// load-bearing); count==0 ⇒ the account was merely quiet for the
	// threshold window and the reconnect was a harmless no-op. Observability
	// only; OfflineSyncCompleted is not a domain event (translateEvent
	// ignores it).
	if osc, ok := rawEvt.(*events.OfflineSyncCompleted); ok {
		a.logger.Info("offline sync completed",
			"count", osc.Count, "profile", a.profile)
	}
	seq := a.eventSeq.Add(1)
	translated, effect, detail := translateEvent(seq, a.nowFn, rawEvt)

	switch effect {
	case sideEffectIgnore:
		return true
	case sideEffectLoggedOut:
		// R-07: full wipe on events.LoggedOut. Dispatch to goroutine so
		// the event handler returns promptly (SynchronousAck=true); the
		// wipe closes the underlying SQLite handles and must not run
		// on the same stack as the handler. panicWg lets Close() wait
		// for this goroutine before its own container close — PR #136.
		a.panicWg.Add(1)
		go func() {
			defer a.panicWg.Done()
			_ = a.Panic(context.Background(), "logged_out:"+detail)
		}()
		evt := domain.PairingEvent{
			ID:    domain.EventID(strconv.FormatUint(seq, 10)),
			TS:    a.nowFn(),
			State: domain.PairFailure,
		}
		return a.enqueueBounded(seq, evt)
	case sideEffectHistorySync:
		// Feature 009: dispatch to background goroutine for download,
		// decode, and batch-insert. Non-blocking — FR-009.
		a.dispatchHistorySync(rawEvt)
		return true
	case sideEffectUnknown:
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "unknown_event", detail)
		return true
	case sideEffectNone:
		if translated == nil {
			return true
		}
		// Spec 110g: track websocket state for the soft-stale watchdog's
		// WebsocketProbe port. Adapter is the only layer that sees the
		// raw events.Connected / events.Disconnected, so the translation
		// boundary is where the atomic flips.
		if ce, ok := translated.(domain.ConnectionEvent); ok {
			a.wsConnected.Store(ce.State == domain.ConnConnected)
		}
		// Persist delivery/read receipts so a caller can confirm a send
		// landed (not just server-accepted) via `wa thread`. Best-effort:
		// a persistence failure must not block event delivery (FR-001).
		if re, ok := translated.(domain.ReceiptEvent); ok {
			a.persistReceipt(re)
		}
		// Signal a waiting Pair() caller on PairSuccess. Non-blocking
		// send into the buffered channel — drop if a previous unread
		// signal is still queued (only one pairing in flight at a time).
		if pe, ok := translated.(domain.PairingEvent); ok && pe.State == domain.PairSuccess {
			select {
			case a.pairSuccessCh <- struct{}{}:
			default:
			}
		}
		// Feature 009 — FR-001: persist inbound MessageEvents to messages.db.
		// Non-blocking: persistence failure MUST NOT prevent event delivery.
		if _, ok := translated.(domain.MessageEvent); ok {
			a.persistInboundMessage(rawEvt)
		}
		if !a.enqueueBounded(seq, translated) {
			return false
		}
		return true
	default:
		return true
	}
}

// persistInboundMessage extracts metadata from the raw whatsmeow event
// and persists it to messages.db via historyContainer.InsertRaw. Called
// from the sideEffectNone branch of handleWAEvent for MessageEvents
// only. Non-blocking: failure is logged but does not prevent event
// delivery. Feature 009 — FR-001.
func (a *Adapter) persistInboundMessage(rawEvt any) {
	if a.history == nil {
		return
	}
	wmEvt, ok := rawEvt.(*events.Message)
	if !ok || wmEvt == nil {
		return
	}

	chatJID := wmEvt.Info.Chat.String()
	senderJID := wmEvt.Info.Sender.String()
	messageID := wmEvt.Info.ID
	ts := wmEvt.Info.Timestamp.Unix()
	pushName := wmEvt.Info.PushName
	isFromMe := wmEvt.Info.IsFromMe

	// Spec 107: capture the addressing-mode duality whatsmeow surfaces
	// on every inbound message. Empty when whatsmeow has not yet
	// learned the mapping; the v5 schema columns are nullable so empty
	// strings hit the database as NULL via the InsertRaw boundary.
	var senderAltJID string
	if !wmEvt.Info.SenderAlt.IsEmpty() {
		senderAltJID = wmEvt.Info.SenderAlt.String()
	}
	addressingMode := string(wmEvt.Info.AddressingMode)

	body, mediaType, caption := extractBodyAndMedia(wmEvt)

	// Marshal the full inner message so media-download can reconstruct the
	// encrypted media hints later (FR-050). proto.Marshal on a nil message
	// is a programming error at this point — wmEvt.Message was already
	// inspected by extractBodyAndMedia.
	var rawProto []byte
	if wmEvt.Message != nil {
		b, marshalErr := proto.Marshal(wmEvt.Message)
		if marshalErr != nil {
			a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg_marshal", marshalErr.Error())
			// Fall through with nil rawProto — row still persists, just
			// media.download will fail with not_cached until a re-sync
			// over HS2 fills in raw_proto.
		} else {
			rawProto = b
		}
	}

	// Issue #201: persist the interactive reply selection (list row /
	// button / native-flow pick) so the client read path can surface which
	// menu option the user chose. nil for ordinary messages → SQL NULL.
	interactiveJSON := a.interactiveJSONForPersist(wmEvt.Message)

	if err := a.history.InsertRawInteractive(
		context.Background(),
		chatJID, senderJID, messageID, ts,
		body, mediaType, caption, pushName, isFromMe, rawProto,
		senderAltJID, addressingMode, interactiveJSON,
	); err != nil {
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg", err.Error())
	}

	// FR-028: refresh the local contact mirror with the pushName the
	// server just advertised. Only for inbound (not self-authored) user
	// JIDs — group JIDs carry the group as Chat and the sender as Sender,
	// so we key on wmEvt.Info.Sender which is always a user JID.
	if a.pushNameSink != nil && !isFromMe && pushName != "" {
		senderDomainJID, jidErr := toDomain(wmEvt.Info.Sender)
		if jidErr == nil && !senderDomainJID.IsZero() && !senderDomainJID.IsGroup() {
			a.pushNameSink(context.Background(), senderDomainJID, pushName)
		}
	}
}

// interactiveJSONForPersist returns the JSON-encoded interactive reply
// selection for msg, or nil when msg carries no interactive reply (issue
// #201). A marshal failure is audited and treated as "no selection" — the
// row still persists with a NULL interactive_json because the body +
// raw_proto are the load-bearing fields and the selection is best-effort.
// Extracted from persistInboundMessage to keep that function below the
// gocyclo ceiling.
func (a *Adapter) interactiveJSONForPersist(msg *waE2E.Message) []byte {
	payload := extractInteractive(msg)
	if payload == nil {
		return nil
	}
	b, err := marshalInteractiveJSON(payload)
	if err != nil {
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg_interactive", err.Error())
		return nil
	}
	return b
}

// SetPushNameRefresher wires an app-layer policy object that receives
// (jid, pushName) for every inbound *events.Message with a non-empty
// pushName. The adapter itself makes no decision about whether to upsert
// — that policy lives in the app layer (FR-028). Calling with nil
// disables the hook. Safe to call once at boot; not safe for concurrent
// updates (composition root calls it exactly once).
func (a *Adapter) SetPushNameRefresher(fn func(ctx context.Context, jid domain.JID, pushName string)) {
	if a == nil {
		return
	}
	a.pushNameSink = fn
}

// extractBodyAndMedia pulls body text, MIME type, and caption from a
// whatsmeow message event. Used by both persistInboundMessage and (in a
// future commit) the history sync translator.
func extractBodyAndMedia(wmEvt *events.Message) (body, mediaType, caption string) {
	if wmEvt.Message == nil {
		return "", "", ""
	}
	if c := wmEvt.Message.GetConversation(); c != "" {
		return c, "", ""
	}
	if ext := wmEvt.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText(), "", ""
	}
	if img := wmEvt.Message.GetImageMessage(); img != nil {
		return img.GetCaption(), img.GetMimetype(), img.GetCaption()
	}
	if doc := wmEvt.Message.GetDocumentMessage(); doc != nil {
		return doc.GetCaption(), doc.GetMimetype(), doc.GetCaption()
	}
	if vid := wmEvt.Message.GetVideoMessage(); vid != nil {
		return vid.GetCaption(), vid.GetMimetype(), vid.GetCaption()
	}
	if aud := wmEvt.Message.GetAudioMessage(); aud != nil {
		return "", aud.GetMimetype(), ""
	}
	return "", "", ""
}

// recordAuditDetail is the internal audit helper used by handleWAEvent
// and the port implementations. It fills in the actor as "whatsmeow" and
// the current timestamp from nowFn.
func (a *Adapter) recordAuditDetail(action domain.AuditAction, subject domain.JID, decision, detail string) {
	_ = a.auditBuf.Record(context.Background(), domain.AuditEvent{
		TS:       a.nowFn(),
		Actor:    "whatsmeow",
		Action:   action,
		Subject:  subject,
		Decision: decision,
		Detail:   detail,
	})
}

// clearSessionLocked is the internal helper that resets the overlay
// session and (when a real sessionContainer is wired) delegates clearing
// to it. The overlay-only path is enough for unit tests.
func (a *Adapter) clearSessionLocked() error {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedSession = domain.Session{}
	return nil
}

// Logout calls the upstream whatsmeow Logout (server-side device unlink).
// It is exposed for the composition root's handlePanic to invoke directly.
// If the client is nil or already closed, Logout returns nil.
func (a *Adapter) Logout(ctx context.Context) error {
	if a.closed.Load() || a.client == nil {
		return nil
	}
	return a.client.Logout(ctx)
}

// --- porttest.Adapter seed surface ---

// SeedContact inserts a contact into the overlay directory used by
// Lookup. Production code never calls this; it exists so the
// //go:build integration contract suite can drive deterministic state.
func (a *Adapter) SeedContact(c domain.Contact) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedContacts[c.JID] = c
}

// SeedGroup inserts a group into the overlay used by List/Get.
func (a *Adapter) SeedGroup(g domain.Group) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedGroups[g.JID] = g
}

// EnqueueEvent pushes an event onto the porttest-only queue.
// Bypasses eventCh (256-cap) so the contract suite's ES6 1000-event
// burst does not drop. Production code does not call this path.
func (a *Adapter) EnqueueEvent(e domain.Event) {
	a.testEvtMu.Lock()
	a.testEvtQ = append(a.testEvtQ, e)
	a.testEvtMu.Unlock()
}

// recordDelivered tracks an event id as delivered so Ack can return
// ErrUnknownEvent for ids that were never handed to the caller (ES5).
// Bounded FIFO eviction at deliveredIDsCap keeps memory flat.
func (a *Adapter) recordDelivered(id domain.EventID) {
	if id.IsZero() {
		return
	}
	a.testEvtMu.Lock()
	defer a.testEvtMu.Unlock()
	if _, ok := a.deliveredIDs[id]; ok {
		return
	}
	a.deliveredIDs[id] = struct{}{}
	a.deliveredList = append(a.deliveredList, id)
	if len(a.deliveredList) > deliveredIDsCap {
		evict := a.deliveredList[0]
		a.deliveredList = a.deliveredList[1:]
		delete(a.deliveredIDs, evict)
	}
}

// isDelivered reports whether the id was previously returned by Next.
func (a *Adapter) isDelivered(id domain.EventID) bool {
	a.testEvtMu.Lock()
	defer a.testEvtMu.Unlock()
	_, ok := a.deliveredIDs[id]
	return ok
}

// popTestEvent pulls the oldest porttest-queued event, if any.
func (a *Adapter) popTestEvent() (domain.Event, bool) {
	a.testEvtMu.Lock()
	defer a.testEvtMu.Unlock()
	if len(a.testEvtQ) == 0 {
		return nil, false
	}
	evt := a.testEvtQ[0]
	a.testEvtQ = a.testEvtQ[1:]
	return evt, true
}

// AppendHistory seeds per-chat history for HS1/HS3 contract clauses.
func (a *Adapter) AppendHistory(chat domain.JID, msg domain.Message) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedHistory[chat] = append(a.seedHistory[chat], msg)
}

// SupportsRemoteBackfill reports whether the adapter can issue an
// on-demand BuildHistorySyncRequest. The whatsmeow adapter returns true;
// the porttest suite uses this to gate HS2.
func (a *Adapter) SupportsRemoteBackfill() bool { return true }
