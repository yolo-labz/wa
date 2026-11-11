package main

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// compositionHandler is a JSON-RPC handler registered at the composition
// root level (outside the app dispatcher). Used for "allow" and "panic"
// which need filesystem I/O and direct adapter access.
type compositionHandler func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// dispatcherAdapter bridges app.Dispatcher to socket.Dispatcher by
// converting app.Event to socket.Event via a goroutine. It also
// intercepts composition-root-level methods before delegating.
type dispatcherAdapter struct {
	d          *app.Dispatcher
	intercepts map[string]compositionHandler
	events     chan socket.Event
	done       chan struct{}

	exec   *app.Executor                      // FR-116-16 dispatch boundary for every transport
	tokens atomic.Pointer[sqlitetokens.Store] // REST token store, for owner revalidation
	audit  app.AuditLog                       // set before any listener starts; audits admin overrides
}

// newDispatcherAdapter constructs the adapter and starts the event
// forwarding goroutine. Cancel ctx to stop it.
func newDispatcherAdapter(ctx context.Context, d *app.Dispatcher, intercepts map[string]compositionHandler) *dispatcherAdapter {
	da := &dispatcherAdapter{
		d:          d,
		intercepts: intercepts,
		events:     make(chan socket.Event, 64),
		done:       make(chan struct{}),
	}
	da.exec = da.newExecutor()
	go da.run(ctx)
	return da
}

// run reads app.Events and converts them to socket.Events until the
// source channel closes or ctx is cancelled.
func (da *dispatcherAdapter) run(ctx context.Context) {
	defer close(da.done)
	src := da.d.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ae, ok := <-src:
			if !ok {
				return
			}
			// Chat/Sender/Body ride along as the FR-060 filter selectors.
			// They are `json:"-"` on both sides and never reach the wire —
			// dropping them here left every subscription with a `--chats`,
			// `--senders` or `--body-re` filter matching zero events.
			//
			// Seq is the FR-061 cursor and does reach the wire. Dropped
			// here it arrived as 0, and both seq-aware paths in the socket
			// server are written to skip on 0: no `seq` field on any
			// notification frame, no stream.drop on a gap, and
			// `subscribe({since})` re-delivering from the top.
			se := socket.Event{
				Type:    ae.Type,
				Seq:     ae.Seq,
				Payload: ae.Payload,
				Chat:    ae.Chat,
				Sender:  ae.Sender,
				Body:    ae.Body,
			}
			select {
			case da.events <- se:
			case <-ctx.Done():
				return
			}
		}
	}
}

// Handle implements socket.Dispatcher and rest.Dispatcher: every call
// runs through the executor (see dispatch).
func (da *dispatcherAdapter) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return da.dispatch(ctx, method, params)
}

// route is one execution's work. Composition-root intercepts are checked
// first; unmatched methods fall through to the app dispatcher.
func (da *dispatcherAdapter) route(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if h, ok := da.intercepts[method]; ok {
		return h(ctx, params)
	}
	return da.d.Handle(ctx, method, params)
}

// Events implements socket.Dispatcher.
// dispatchTo is the app dispatcher's method as a compositionHandler, for
// intercepts that wrap a method rather than replace it.
func dispatchTo(d *app.Dispatcher, method string) compositionHandler {
	return func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
		return d.Handle(ctx, method, params)
	}
}

func (da *dispatcherAdapter) Events() <-chan socket.Event {
	return da.events
}

// Close cancels every execution and waits for the forwarding goroutine.
func (da *dispatcherAdapter) Close() {
	da.exec.Close()
	<-da.done
}

// socketCaller is the owner-only profile socket's dispatcher, and the one
// place the socket principal is built (FR-116-16): REST and /mcp attribute
// calls in their authenticators and never reach it.
type socketCaller struct {
	*dispatcherAdapter
	profile string
}

func (s socketCaller) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return s.dispatcherAdapter.Handle(app.WithCaller(ctx, app.SocketPrincipal(s.profile)), method, params)
}
