package main

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/internal/app"
)

// dispatcherAdapter bridges app.Dispatcher to socket.Dispatcher by
// converting app.Event to socket.Event via a goroutine.
type dispatcherAdapter struct {
	d      *app.Dispatcher
	events chan socket.Event
	done   chan struct{}
}

// newDispatcherAdapter constructs the adapter and starts the event
// forwarding goroutine. Cancel ctx to stop it.
func newDispatcherAdapter(ctx context.Context, d *app.Dispatcher) *dispatcherAdapter {
	da := &dispatcherAdapter{
		d:      d,
		events: make(chan socket.Event, 64),
		done:   make(chan struct{}),
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

// Handle implements socket.Dispatcher.
func (da *dispatcherAdapter) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
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
