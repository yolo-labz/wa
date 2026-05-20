package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
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
	// ProfileEditor implements profile.setName / profile.setStatus /
	// profile.avatar (FR-026, FR-027, FR-028). Nil is allowed —
	// method_not_found.
	ProfileEditor ProfileEditor
	// GroupAdmin implements group.create / group.leave / group.addParticipants
	// / group.removeParticipants / group.promote / group.demote / group.edit /
	// group.inviteGet / group.inviteRevoke / group.inviteJoin (FR-020..FR-025).
	// Nil is allowed — method_not_found.
	GroupAdmin GroupAdmin
	// Polls implements poll.vote (FR-032). Nil is allowed — method_not_found.
	// v2.0.0 impl returns -32000 upstream_error until whatsmeow exposes an
	// outbound Vote helper.
	Polls PollManager
	// Identity implements contact.resolve (PN ↔ LID translation) per
	// spec 106. Nil is allowed — method_not_found. Backed in production
	// by `whatsmeow.Client.Store.LIDs`; tests use a deterministic
	// in-memory map.
	Identity IdentityResolver
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
	Idempotency IdempotencyStore
	// Transcriber is the spec-110h port for voice-note speech-to-text.
	// Nil is allowed — media.download with transcribe=true on an audio/*
	// payload returns -32115 transcriber_not_configured. The composition
	// root selects an adapter from WA_TRANSCRIBER (whispercpp | hear | groq).
	Transcriber Transcriber
	// Transcripts is the spec-110h v6 persistence sidecar. Nil is allowed
	// when Transcriber is also nil; otherwise transcripts are computed
	// per-call without dedup/hydration. Production wires both.
	Transcripts TranscriptStore
	// TranscriberName is the lowercase selector for the wired Transcriber
	// ("whispercpp" | "hear" | "groq"). Stamped onto TranscriptRecord.Adapter
	// and the synthetic media.transcribed event payload so operators can
	// audit which adapter produced which row. Empty when Transcriber is nil.
	TranscriberName string
	Features        FeatureFlags
	Profile         string
	SessionCreated  time.Time
	Logger          *slog.Logger
	// Websocket is the spec-110g WebsocketProbe used by handleHealth to
	// distinguish a hard websocket disconnect from a silent stall. When
	// nil the health response omits the live websocket field and falls
	// back to the pre-110g "session exists" approximation.
	Websocket WebsocketProbe
	// SoftStaleThresholdSec is the spec-110g threshold (seconds) past
	// which a session with no inbound events transitions to softStale.
	// Zero leaves the watchdog inert; the production composition root
	// reads `WA_SOFT_STALE_THRESHOLD_SEC` and clamps to [30, 3600] or 0.
	SoftStaleThresholdSec int
	// Safety is an optional override for the allowlist + rate-limit pipeline.
	// When non-nil the dispatcher reuses it — letting the composition root
	// share one rate-limit budget with the schedule firer (T1-10 R-03).
	// When nil the dispatcher constructs its own from Allowlist +
	// SessionCreated, preserving the pre-018 default.
	Safety *SafetyPipeline
	// Quoted is the secondary port used by send.listResponse /
	// send.buttonResponse to hydrate ContextInfo.QuotedMessage from
	// the on-disk raw_proto blob (#163). Nil is allowed — those two
	// methods return ErrQuotedMessageStoreNotConfigured. Production
	// wires the sqlitehistory adapter.
	Quoted QuotedMessageStore
}

// Dispatcher is the central orchestrator that routes JSON-RPC method
// names to use case handlers. It holds all 8 port references, the safety
// pipeline, the event bridge, and the method table.
//
// It is safe for concurrent use by multiple goroutines: the method table
// is immutable after construction, the safety pipeline is thread-safe,
// and individual handlers only use their injected port references (which
// are themselves documented as concurrency-safe).
//
// # Architecture pattern (spec 016 T082)
//
// Dispatcher is a Mediator pattern at v0: a single struct holds every
// port reference and demultiplexes inbound JSON-RPC method strings to
// handler methods via a built-in table. With ~80 methods spanning
// send / read / pair / allow / privacy / group / schedule / draft /
// poll / labels / embeddings, the mediator is at the upper edge of
// the readable range Three Dots Labs documents in their CQRS
// migration playbook (https://threedots.tech/post/microservices-or-monolith-its-detail/).
//
// Acceptance criterion: as long as exactly one Dispatcher is the
// authoritative inbound gateway, the table-driven mediator is the
// right shape. The migration trigger to per-handler CQRS commands
// (one struct per write, one struct per read, an outbox for events)
// fires when ANY of these holds:
//
//   - Cross-cutting concerns (auth, audit, retry) start needing
//     per-handler customization beyond what middleware can express
//     declaratively in the table.
//   - The dispatcher_test.go file grows past ~1.5K lines and
//     individual handler tests start needing per-handler test
//     fixtures beyond the shared `newTestDispatcher` helper.
//   - A second primary adapter (REST, MCP, Channel) needs to invoke
//     a SUBSET of the dispatcher's surface — at that point per-
//     handler command structs become a cleaner authorization
//     boundary than the current method-string + scope table.
//
// None of those triggers is hit at v0.5; the mediator stays. This
// comment exists so future maintainers know the pattern is
// deliberate, not accidental, and have a falsifiable migration cue.
type Dispatcher struct {
	sender          MessageSender
	events          EventStream
	contacts        ContactDirectory
	groups          GroupManager
	session         SessionStore
	allowlist       Allowlist
	audit           AuditLog
	history         HistoryStore
	pairer          Pairer
	drafts          DraftStore
	media           MediaStore
	presenceSender  any // nil when PresenceSender not wired
	scheduled       ScheduledStore
	scheduleRunner  *ScheduleRunner
	labels          LabelManager
	moderator       MessageModerator
	chatState       ChatStateManager
	blocker         Blocker
	privacy         PrivacySettings
	sessionTerm     SessionTerminator
	profileEd       ProfileEditor
	groupAdmin      GroupAdmin
	polls           PollManager
	identity        IdentityResolver
	isBusiness      bool
	hybrid          *HybridSearcher
	vectorIndex     VectorIndex
	embedder        Embedder
	idempotency     IdempotencyStore
	transcriber     Transcriber
	transcripts     TranscriptStore
	transcriberName string
	features        FeatureFlags
	profile         string
	websocket       WebsocketProbe
	softStaleSec    int
	safety          *SafetyPipeline
	quoted          QuotedMessageStore
	bridge          *EventBridge
	methods         map[string]methodHandler
	log             *slog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
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
	bridge.SetProfile(cfg.Profile)

	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		sender:          cfg.Sender,
		events:          cfg.Events,
		contacts:        cfg.Contacts,
		groups:          cfg.Groups,
		session:         cfg.Session,
		allowlist:       cfg.Allowlist,
		audit:           cfg.Audit,
		history:         cfg.History,
		pairer:          cfg.Pairer,
		drafts:          cfg.Drafts,
		media:           cfg.Media,
		presenceSender:  cfg.Presence,
		scheduled:       cfg.Scheduled,
		scheduleRunner:  cfg.ScheduleRunner,
		labels:          cfg.Labels,
		moderator:       cfg.Moderator,
		chatState:       cfg.ChatState,
		blocker:         cfg.Blocker,
		privacy:         cfg.Privacy,
		sessionTerm:     cfg.SessionTerm,
		profileEd:       cfg.ProfileEditor,
		groupAdmin:      cfg.GroupAdmin,
		polls:           cfg.Polls,
		identity:        cfg.Identity,
		isBusiness:      cfg.IsBusinessAccount,
		hybrid:          cfg.Hybrid,
		vectorIndex:     cfg.VectorIndex,
		embedder:        cfg.Embedder,
		idempotency:     cfg.Idempotency,
		transcriber:     cfg.Transcriber,
		transcripts:     cfg.Transcripts,
		transcriberName: cfg.TranscriberName,
		features:        cfg.Features,
		profile:         cfg.Profile,
		websocket:       cfg.Websocket,
		softStaleSec:    cfg.SoftStaleThresholdSec,
		safety:          sp,
		quoted:          cfg.Quoted,
		bridge:          bridge,
		log:             cfg.Logger,
		ctx:             ctx,
		cancel:          cancel,
	}

	d.methods = map[string]methodHandler{
		"send":                     d.handleSend,
		"sendMedia":                d.handleSendMedia,
		"react":                    d.handleReact,
		"pair":                     d.handlePair,
		"wait":                     d.handleWait,
		"status":                   d.handleStatus,
		"health":                   d.handleHealth,
		"groups":                   d.handleGroups,
		"markRead":                 d.handleMarkRead,
		"contacts.lookup":          d.handleContactsLookup,
		"contacts.search":          d.handleContactsSearch,
		"contacts.annotate":        d.handleContactsAnnotate,
		"contacts.sync":            d.handleContactsSync,
		"contacts.resolve.confirm": d.handleContactsResolveConfirm,
		"presence.composing.start": d.handlePresenceComposingStart,
		"presence.composing.stop":  d.handlePresenceComposingStop,
		"presence.recording.start": d.handlePresenceRecordingStart,
		"presence.recording.stop":  d.handlePresenceRecordingStop,
		"thread.get":               d.handleThreadGet,
		"messages.search":          d.handleMessagesSearch,
		"draft.list":               d.handleDraftList,
		"draft.get":                d.handleDraftGet,
		"draft.approve":            d.handleDraftApprove,
		"draft.reject":             d.handleDraftReject,
		"media.resolve":            d.handleMediaResolve,
		"media.download":           d.handleMediaDownload,
		"media.gc":                 d.handleMediaGC,
		"send.reply":               d.handleSendReply,
		"send.listResponse":        d.handleSendListResponse,
		"send.buttonResponse":      d.handleSendButtonResponse,
		"chat.composing":           d.handleComposing,
		"groups.get":               d.handleGroupsGet,
		"schedule.send":            d.handleScheduleSend,
		"schedule.list":            d.handleScheduleList,
		"schedule.cancel":          d.handleScheduleCancel,
		"schedule.update":          d.handleScheduleUpdate,
		"labels.list":              d.handleLabelsList,
		"labels.create":            d.handleLabelsCreate,
		"labels.delete":            d.handleLabelsDelete,
		"labels.assign":            d.handleLabelsAssign,
		"labels.unassign":          d.handleLabelsUnassign,
		"embeddings.status":        d.handleEmbeddingsStatus,
		"embeddings.purge":         d.handleEmbeddingsPurge,
		"message.revoke":           d.handleMessageRevoke,
		"message.edit":             d.handleMessageEdit,
		"message.forward":          d.handleSendForward,
		"message.star":             d.handleSendStar,
		"message.setDisappearing":  d.handleSetDisappearing,
		"chat.archive":             d.handleChatArchive,
		"chat.mute":                d.handleChatMute,
		"chat.pin":                 d.handleChatPin,
		"chat.markUnread":          d.handleChatMarkUnread,
		"contact.block":            d.handleContactBlock,
		"contact.unblock":          d.handleContactUnblock,
		"contact.blocklist":        d.handleContactBlocklist,
		"privacy.set":              d.handlePrivacySet,
		"privacy.get":              d.handlePrivacyGet,
		"session.logoutAll":        d.handleSessionLogoutAll,
		"session.logout":           d.handleSessionLogout,
		"profile.setName":          d.handleProfileSetName,
		"profile.setStatus":        d.handleProfileSetStatus,
		"contacts.profilePhoto":    d.handleProfileAvatar,
		"group.create":             d.handleGroupCreate,
		"group.leave":              d.handleGroupLeave,
		"group.addParticipants":    d.handleGroupAddParticipants,
		"group.removeParticipants": d.handleGroupRemoveParticipants,
		"group.promote":            d.handleGroupPromote,
		"group.demote":             d.handleGroupDemote,
		"group.edit":               d.handleGroupEdit,
		"group.inviteGet":          d.handleGroupInviteGet,
		"group.inviteRevoke":       d.handleGroupInviteRevoke,
		"group.inviteJoin":         d.handleGroupInviteJoin,
		"poll.vote":                d.handlePollVote,
		"contact.resolve":          d.handleContactResolve,
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

// Bridge returns the dispatcher's EventBridge so the composition root
// can wire goroutines that publish synthetic events (spec 110g
// soft-stale watchdog) or read the last-event timestamp. Returned
// pointer is owned by the dispatcher; callers must not Close it.
func (d *Dispatcher) Bridge() *EventBridge { return d.bridge }

// Methods returns the names of every JSON-RPC method registered on
// the dispatcher, sorted alphabetically. Composition-root intercepts
// (admin.reload, admin.audit.rotate, debug.pprof.profile,
// config.features) are NOT included — they live in the dispatcherAdapter
// table in cmd/wad/main.go. Issue #158 — used by the REST scope-map
// invariant test to assert every dispatcher method has a MethodScopes
// entry, so a future handler addition cannot silently fall through to
// the -32099 default-deny path.
func (d *Dispatcher) Methods() []string {
	out := make([]string, 0, len(d.methods))
	for name := range d.methods {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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

// SubscribeStream returns a long-lived subscriber channel for the
// SSE primary adapter (spec 110b). bufSize gates per-subscriber
// backpressure; full channels drop oldest events. The returned
// cancel function MUST be called when the subscriber goes away
// (typically deferred from the SSE handler's request scope).
func (d *Dispatcher) SubscribeStream(filter []string, bufSize int) (ch <-chan Event, cancel func()) {
	return d.bridge.SubscribeStream(filter, bufSize)
}

// Close cancels the dispatcher's context, stops the event bridge, and
// waits for the bridge goroutine to exit.
func (d *Dispatcher) Close() error {
	d.cancel()
	d.bridge.Close()
	return nil
}
