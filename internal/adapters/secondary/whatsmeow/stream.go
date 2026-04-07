package whatsmeow

import (
	"context"

	"github.com/yolo-labz/wa/internal/domain"
)

// Next implements app.EventStream. It blocks until an event is available
// on eventCh or ctx is done, whichever comes first. Per ports.go §ES2,
// ctx cancellation returns ctx.Err().
func (a *Adapter) Next(ctx context.Context) (domain.Event, error) {
	select {
	case evt := <-a.eventCh:
		return evt, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.clientCtx.Done():
		return nil, a.clientCtx.Err()
	}
}

// Ack implements app.EventStream. The whatsmeow adapter uses
// SynchronousAck=true at the transport layer, which means whatsmeow has
// already acked the upstream message by the time Next returns. The
// domain-level Ack is therefore a no-op for known ids; per ES5 it must
// still return a typed error for an unknown (zero) id.
func (a *Adapter) Ack(id domain.EventID) error {
	if id.IsZero() {
		return ErrUnknownEvent
	}
	return nil
}
