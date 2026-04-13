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
	"sync"
	"sync/atomic"
	"time"

	waClient "go.mau.fi/whatsmeow"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/domain"
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
	InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool) error
	Search(ctx context.Context, query string, limit int) ([]domain.Message, error)
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

	// closed flips to true exactly once from Close() to make it safe to
	// call repeatedly.
	closed atomic.Bool

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
}

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
	real := waClient.NewClient(device, NewSlogLogger(logger))

	// Step 4: apply the 8 production flags in one place.
	applyProductionFlags(real)

	// Step 5: detached context. This is the critical CLAUDE.md §"Daemon"
	// invariant: the whatsmeow client lifetime is NOT tied to parentCtx.
	clientCtx, clientCancel := context.WithCancel(context.Background())

	a := &Adapter{
		client:        &realClient{Client: real},
		session:       session,
		history:       history,
		allowlist:     allowlist,
		auditBuf:      newAuditRing(1000),
		logger:        logger,
		clientCtx:     clientCtx,
		clientCancel:  clientCancel,
		eventCh:       make(chan domain.Event, 256),
		nowFn:         time.Now,
		seedContacts:  make(map[domain.JID]domain.Contact),
		seedGroups:    make(map[domain.JID]domain.Group),
		seedHistory:   make(map[domain.JID][]domain.Message),
		pairSuccessCh: make(chan struct{}, 1),
		historySyncCh: make(chan any, historySyncChCap),
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
		nowFn:         nowFn,
		seedContacts:  make(map[domain.JID]domain.Contact),
		seedGroups:    make(map[domain.JID]domain.Group),
		seedHistory:   make(map[domain.JID][]domain.Message),
		pairSuccessCh: make(chan struct{}, 1),
		historySyncCh: make(chan any, historySyncChCap),
	}
	a.historySyncWg.Add(1)
	go a.runHistorySyncWorker(clientCtx)
	a.client.AddEventHandlerWithSuccessStatus(a.handleWAEvent)
	return a
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
	seq := a.eventSeq.Add(1)
	translated, effect, detail := translateEvent(seq, a.nowFn, rawEvt)

	switch effect {
	case sideEffectIgnore:
		return true
	case sideEffectLoggedOut:
		// Clear session state and surface PairFailure to subscribers.
		a.recordAuditDetail(domain.AuditPair, domain.JID{}, "logged_out", detail)
		// Best-effort session clear — real wiring in commit 6 will
		// delete the row.
		_ = a.clearSessionLocked()
		evt := domain.PairingEvent{
			ID:    domain.EventID(fmt.Sprintf("%d", seq)),
			TS:    a.nowFn(),
			State: domain.PairFailure,
		}
		return a.enqueue(evt)
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
		if !a.enqueue(translated) {
			a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "eventch_full", fmt.Sprintf("dropped seq=%d", seq))
			return false
		}
		return true
	default:
		return true
	}
}

// enqueue pushes an event onto eventCh, honouring clientCtx.Done. It
// returns false iff the push failed (buffer full or context cancelled).
// The select is non-blocking on the buffer so a slow consumer becomes
// visible immediately rather than blocking the whatsmeow dispatch loop.
func (a *Adapter) enqueue(evt domain.Event) bool {
	select {
	case a.eventCh <- evt:
		return true
	case <-a.clientCtx.Done():
		return false
	default:
		return false
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

	body, mediaType, caption := extractBodyAndMedia(wmEvt)

	if err := a.history.InsertRaw(context.Background(),
		chatJID, senderJID, messageID, ts,
		body, mediaType, caption, pushName, isFromMe,
	); err != nil {
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg", err.Error())
	}
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

// EnqueueEvent pushes an event onto the stream (porttest surface).
func (a *Adapter) EnqueueEvent(e domain.Event) {
	// Best-effort; tests should not overflow the 256-cap buffer.
	select {
	case a.eventCh <- e:
	default:
	}
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
