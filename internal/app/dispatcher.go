package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// methodHandler is the function signature for a registered JSON-RPC handler.
type methodHandler func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// DispatcherConfig holds the dependencies for constructing an Dispatcher.
type DispatcherConfig struct {
	Sender         MessageSender
	Events         EventStream
	Contacts       ContactDirectory
	Groups         GroupManager
	Session        SessionStore
	Allowlist      Allowlist
	Audit          AuditLog
	History        HistoryStore
	Pairer         Pairer
	Drafts         DraftStore
	Media          MediaStore
	Presence       PresenceSender
	Scheduled      ScheduledStore
	ScheduleRunner *ScheduleRunner
	Labels         LabelManager
	// Moderator implements message.revoke + message.edit (FR-014/FR-015).
	// Nil is allowed — the dispatcher returns method_not_found for those
	// methods, matching the ReplySender / PresenceSender pattern.
	Moderator MessageModerator
	// ChatState implements chat.archive / chat.mute / chat.pin /
	// chat.markUnread (FR-016, FR-017). Nil is allowed — method_not_found.
	ChatState ChatStateManager
	// Blocker implements contact.block / contact.unblock /
	// contact.blocklist (FR-018, FR-019). Nil is allowed — method_not_found.
	// When non-nil, send / sendMedia / send.reply consult ListBlocked and
	// refuse a blocked target with -32100 policy_refused.
	Blocker Blocker
	// Privacy implements privacy.set / privacy.get (FR-029, FR-030). Nil is
	// allowed — method_not_found.
	Privacy PrivacySettings
	// SessionTerm implements session.logoutAll (FR-031). Nil is allowed —
	// method_not_found. Current impl returns -32000 upstream_error until
	// whatsmeow gains a LogoutAll helper.
	SessionTerm SessionTerminator
	// IsBusinessAccount reports whether the paired device is a WhatsApp
	// Business account. Personal accounts receive -32114 for any labels.*
	// call regardless of the Labels feature flag. Feature 017 T3-22.
	IsBusinessAccount bool
	// Hybrid is the optional FR-101 hybrid retriever. When nil or when the
	// embeddings flag is off, messages.search mode=hybrid degrades to
	// BM25 with a user-visible hint; mode=vector returns -32113. T3-11.
	Hybrid *HybridSearcher
	// VectorIndex + Embedder are optional direct handles used by the
	// embeddings.* admin methods (status, purge). When either is nil
	// the methods report embeddings_disabled. T3-13.
	VectorIndex VectorIndex
	Embedder    Embedder
	// Idempotency is the FR-034a sidecar used by every mutating handler
	// (send, sendMedia, react, markRead, pair, schedule.send, send.reply).
	// When nil the dispatcher bypasses replay-caching entirely — handlers
	// run verbatim regardless of the idempotencyKey in params.
	Idempotency    IdempotencyStore
	Features       FeatureFlags
	Profile        string
	SessionCreated time.Time
	Logger         *slog.Logger
	// Safety is an optional override for the allowlist + rate-limit pipeline.
	// When non-nil the dispatcher reuses it — letting the composition root
	// share one rate-limit budget with the schedule firer (T1-10 R-03).
	// When nil the dispatcher constructs its own from Allowlist +
	// SessionCreated, preserving the pre-018 default.
	Safety *SafetyPipeline
}

// Dispatcher is the central orchestrator that routes JSON-RPC method
// names to use case handlers. It holds all 8 port references, the safety
// pipeline, the event bridge, and the method table.
//
// It is safe for concurrent use by multiple goroutines: the method table
// is immutable after construction, the safety pipeline is thread-safe,
// and individual handlers only use their injected port references (which
// are themselves documented as concurrency-safe).
type Dispatcher struct {
	sender         MessageSender
	events         EventStream
	contacts       ContactDirectory
	groups         GroupManager
	session        SessionStore
	allowlist      Allowlist
	audit          AuditLog
	history        HistoryStore
	pairer         Pairer
	drafts         DraftStore
	media          MediaStore
	presenceSender any // nil when PresenceSender not wired
	scheduled      ScheduledStore
	scheduleRunner *ScheduleRunner
	labels         LabelManager
	moderator      MessageModerator
	chatState      ChatStateManager
	blocker        Blocker
	privacy        PrivacySettings
	sessionTerm    SessionTerminator
	isBusiness     bool
	hybrid         *HybridSearcher
	vectorIndex    VectorIndex
	embedder       Embedder
	idempotency    IdempotencyStore
	features       FeatureFlags
	profile        string
	safety         *SafetyPipeline
	bridge         *EventBridge
	methods        map[string]methodHandler
	log            *slog.Logger
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewDispatcher constructs an Dispatcher with all 8 ports, the
// safety pipeline (allowlist + rate limiter with warmup), the event bridge,
// and a populated method table. It starts the bridge goroutine.
func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	sp := cfg.Safety
	if sp == nil {
		rl := NewRateLimiter(cfg.SessionCreated)
		sp = NewSafetyPipeline(cfg.Allowlist, rl)
	}
	bridge := NewEventBridge(cfg.Events, cfg.Logger)

	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		sender:         cfg.Sender,
		events:         cfg.Events,
		contacts:       cfg.Contacts,
		groups:         cfg.Groups,
		session:        cfg.Session,
		allowlist:      cfg.Allowlist,
		audit:          cfg.Audit,
		history:        cfg.History,
		pairer:         cfg.Pairer,
		drafts:         cfg.Drafts,
		media:          cfg.Media,
		presenceSender: cfg.Presence,
		scheduled:      cfg.Scheduled,
		scheduleRunner: cfg.ScheduleRunner,
		labels:         cfg.Labels,
		moderator:      cfg.Moderator,
		chatState:      cfg.ChatState,
		blocker:        cfg.Blocker,
		privacy:        cfg.Privacy,
		sessionTerm:    cfg.SessionTerm,
		isBusiness:     cfg.IsBusinessAccount,
		hybrid:         cfg.Hybrid,
		vectorIndex:    cfg.VectorIndex,
		embedder:       cfg.Embedder,
		idempotency:    cfg.Idempotency,
		features:       cfg.Features,
		profile:        cfg.Profile,
		safety:         sp,
		bridge:         bridge,
		log:            cfg.Logger,
		ctx:            ctx,
		cancel:         cancel,
	}

	d.methods = map[string]methodHandler{
		"send":              d.handleSend,
		"sendMedia":         d.handleSendMedia,
		"react":             d.handleReact,
		"pair":              d.handlePair,
		"wait":              d.handleWait,
		"status":            d.handleStatus,
		"groups":            d.handleGroups,
		"markRead":          d.handleMarkRead,
		"contacts.lookup":   d.handleContactsLookup,
		"contacts.search":   d.handleContactsSearch,
		"contacts.annotate": d.handleContactsAnnotate,
		"contacts.sync":     d.handleContactsSync,
		"thread.get":        d.handleThreadGet,
		"messages.search":   d.handleMessagesSearch,
		"draft.list":        d.handleDraftList,
		"draft.get":         d.handleDraftGet,
		"draft.approve":     d.handleDraftApprove,
		"draft.reject":      d.handleDraftReject,
		"media.resolve":     d.handleMediaResolve,
		"media.download":    d.handleMediaDownload,
		"media.gc":          d.handleMediaGC,
		"send.reply":        d.handleSendReply,
		"chat.composing":    d.handleComposing,
		"groups.get":        d.handleGroupsGet,
		"schedule.send":     d.handleScheduleSend,
		"schedule.list":     d.handleScheduleList,
		"schedule.cancel":   d.handleScheduleCancel,
		"schedule.update":   d.handleScheduleUpdate,
		"labels.list":       d.handleLabelsList,
		"labels.create":     d.handleLabelsCreate,
		"labels.delete":     d.handleLabelsDelete,
		"labels.assign":     d.handleLabelsAssign,
		"labels.unassign":   d.handleLabelsUnassign,
		"embeddings.status": d.handleEmbeddingsStatus,
		"embeddings.purge":  d.handleEmbeddingsPurge,
		"message.revoke":    d.handleMessageRevoke,
		"message.edit":      d.handleMessageEdit,
		"chat.archive":      d.handleChatArchive,
		"chat.mute":         d.handleChatMute,
		"chat.pin":          d.handleChatPin,
		"chat.markUnread":   d.handleChatMarkUnread,
		"contact.block":     d.handleContactBlock,
		"contact.unblock":   d.handleContactUnblock,
		"contact.blocklist": d.handleContactBlocklist,
		"privacy.set":       d.handlePrivacySet,
		"privacy.get":       d.handlePrivacyGet,
		"session.logoutAll": d.handleSessionLogoutAll,
	}

	go bridge.Run()

	return d
}

// SetKnownRecipientFunc configures the per-recipient rate limiter's
// new-recipient detection callback. Called by the composition root after
// dispatcher construction. Feature 009 — FR-032.
func (d *Dispatcher) SetKnownRecipientFunc(fn KnownRecipientFunc) {
	d.safety.limiter.SetKnownRecipientFunc(fn)
}

// RegisterMethod adds a JSON-RPC method handler to the dispatcher's
// method table. This allows the composition root to register adapter-layer
// methods (e.g., history, messages, search) without importing adapter
// packages in internal/app. Feature 009.
func (d *Dispatcher) RegisterMethod(name string, h func(context.Context, json.RawMessage) (json.RawMessage, error)) {
	d.methods[name] = h
}

// Handle routes a JSON-RPC method call to the appropriate handler.
// Unknown methods return ErrMethodNotFound.
func (d *Dispatcher) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	h, ok := d.methods[method]
	if !ok {
		return nil, ErrMethodNotFound
	}
	return h(ctx, params)
}

// Events returns the event bridge's output channel.
func (d *Dispatcher) Events() <-chan Event {
	return d.bridge.Events()
}

// Close cancels the dispatcher's context, stops the event bridge, and
// waits for the bridge goroutine to exit.
func (d *Dispatcher) Close() error {
	d.cancel()
	d.bridge.Close()
	return nil
}
