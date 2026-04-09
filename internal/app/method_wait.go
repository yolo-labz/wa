package app

import (
	"context"
	"encoding/json"
	"time"
)

// waitParams is the JSON-RPC params for the "wait" method (FR-029).
type waitParams struct {
	Events    []string `json:"events,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// handleWait implements the "wait" JSON-RPC method (FR-029..FR-031).
//
// It registers a waiter on the event bridge with an optional event type
// filter and blocks until a matching event arrives or the timeout fires.
// The default timeout is 30 seconds; the default filter is all events.
func (d *Dispatcher) handleWait(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p waitParams
	// Nil/empty params are valid — defaults apply.
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}

	timeoutMs := p.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	ch, cancel := d.bridge.RegisterWaiter(p.Events)
	defer cancel()

	tctx, tcancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer tcancel()

	select {
	case evt := <-ch:
		return json.Marshal(evt)
	case <-tctx.Done():
		return nil, ErrWaitTimeout
	}
}
