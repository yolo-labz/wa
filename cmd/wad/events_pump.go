package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// eventsPumpBuffer sizes the pump's fan-out subscription. The pump is a
// peer subscriber of the SSE/socket waiters on the same EventBridge
// fan-out — a full buffer drops events for the DURABLE ring only, never
// for live subscribers, so the deep buffer trades a few hundred KiB of
// worst-case memory for append completeness during inbound bursts.
const eventsPumpBuffer = 1024

// eventsPumpSource is the minimal stream surface the pump consumes.
// *app.Dispatcher satisfies it.
type eventsPumpSource interface {
	SubscribeStream(filter []string, bufSize int) (<-chan app.Event, func())
}

// startEventsPump bridges the live EventBridge fan-out into the durable
// sqlite events ring (ARCH-01: app.EventBuffer finally has its producer).
// Every translated event — the same subscriber-safe projection the
// socket and SSE paths deliver — is appended as an EventRecord under the
// seq the bridge already stamped on it, not one the ring invents; GET
// /v1/events replays the ring for Last-Event-ID resume, and a client that
// resumes from a seq it read off the socket must land on the same row.
//
// A consequence worth naming: the pump is a lossy peer subscriber, so a
// full buffer now leaves a HOLE in the ring's seq column instead of
// silently renumbering around the loss. That is the honest encoding — a
// gap in the ids a replay hands back is exactly the event that went
// missing.
//
// Best-effort by design: a marshal or append failure logs and continues
// (the live fan-out must never block on disk), and a nil store returns a
// no-op stop so a degraded events.db keeps the daemon up (SF-03 posture).
// The returned stop func deregisters the subscription and waits for the
// pump goroutine to exit; call it BEFORE closing the events store.
func startEventsPump(ctx context.Context, src eventsPumpSource, store app.EventBuffer, log *slog.Logger) func() {
	if store == nil {
		return func() {}
	}
	ch, cancel := src.SubscribeStream(nil, eventsPumpBuffer)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				data, err := json.Marshal(evt.Payload)
				if err != nil {
					log.Warn("events pump: marshal", "type", evt.Type, "err", err)
					continue
				}
				rec := app.EventRecord{Seq: evt.Seq, Kind: evt.Type, TrustedJSON: data}
				if err := store.Append(ctx, rec); err != nil {
					if ctx.Err() != nil {
						return // shutdown race, not a real append failure
					}
					log.Warn("events pump: append", "type", evt.Type, "err", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		close(stop)
		<-done
	}
}
