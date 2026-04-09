package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// methodHandler is the function signature for a registered JSON-RPC handler.
type methodHandler func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// AppDispatcherConfig holds the dependencies for constructing an AppDispatcher.
type AppDispatcherConfig struct {
	Sender         MessageSender
	Events         EventStream
	Contacts       ContactDirectory
	Groups         GroupManager
	Session        SessionStore
	Allowlist      Allowlist
	Audit          AuditLog
	History        HistoryStore
	SessionCreated time.Time
	Logger         *slog.Logger
}

// AppDispatcher is the central orchestrator that routes JSON-RPC method
// names to use case handlers. It holds all 8 port references, the safety
// pipeline, the event bridge, and the method table.
//
// It is safe for concurrent use by multiple goroutines: the method table
// is immutable after construction, the safety pipeline is thread-safe,
// and individual handlers only use their injected port references (which
// are themselves documented as concurrency-safe).
type AppDispatcher struct {
	sender   MessageSender
	events   EventStream
	contacts ContactDirectory
	groups   GroupManager
	session  SessionStore
	allowlist Allowlist
	audit    AuditLog
	history  HistoryStore
	safety   *SafetyPipeline
	bridge   *EventBridge
	methods  map[string]methodHandler
	log      *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewAppDispatcher constructs an AppDispatcher with all 8 ports, the
// safety pipeline (allowlist + rate limiter with warmup), the event bridge,
// and a populated method table. It starts the bridge goroutine.
func NewAppDispatcher(cfg AppDispatcherConfig) *AppDispatcher {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	rl := NewRateLimiter(cfg.SessionCreated)
	sp := NewSafetyPipeline(cfg.Allowlist, rl)
	bridge := NewEventBridge(cfg.Events, cfg.Logger)

	ctx, cancel := context.WithCancel(context.Background())

	d := &AppDispatcher{
		sender:    cfg.Sender,
		events:    cfg.Events,
		contacts:  cfg.Contacts,
		groups:    cfg.Groups,
		session:   cfg.Session,
		allowlist: cfg.Allowlist,
		audit:     cfg.Audit,
		history:   cfg.History,
		safety:    sp,
		bridge:    bridge,
		log:       cfg.Logger,
		ctx:       ctx,
		cancel:    cancel,
	}

	d.methods = map[string]methodHandler{
		"send":      d.handleSend,
		"sendMedia": d.handleSendMedia,
		"react":     d.handleReact,
	}

	go bridge.Run()

	return d
}

// Handle routes a JSON-RPC method call to the appropriate handler.
// Unknown methods return ErrMethodNotFound.
func (d *AppDispatcher) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	h, ok := d.methods[method]
	if !ok {
		return nil, ErrMethodNotFound
	}
	return h(ctx, params)
}

// Events returns the event bridge's output channel.
func (d *AppDispatcher) Events() <-chan AppEvent {
	return d.bridge.Events()
}

// Close cancels the dispatcher's context, stops the event bridge, and
// waits for the bridge goroutine to exit.
func (d *AppDispatcher) Close() error {
	d.cancel()
	d.bridge.Close()
	return nil
}
