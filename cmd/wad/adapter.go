package main

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/app"
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
			se := socket.Event{
				Type:    ae.Type,
				Payload: ae.Payload,
			}
			select {
			case da.events <- se:
			case <-ctx.Done():
				return
			}
		}
	}
}

// Handle implements socket.Dispatcher. Composition-root intercepts are
// checked first; unmatched methods fall through to the app dispatcher.
func (da *dispatcherAdapter) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if h, ok := da.intercepts[method]; ok {
		return h(ctx, params)
	}
	return da.d.Handle(ctx, method, params)
}

// Events implements socket.Dispatcher.
func (da *dispatcherAdapter) Events() <-chan socket.Event {
	return da.events
}

// Close waits for the forwarding goroutine to exit.
func (da *dispatcherAdapter) Close() {
	<-da.done
}
