package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// restEventStreamAdapter wraps the daemon's app.Dispatcher so the
// rest package can subscribe to events without importing
// internal/app's Event type. Spec 110b — translates app.Event into
// the wire-ready rest.Event shape. Per-event sequence numbers come
// from a monotonic counter scoped to the subscriber (each SSE
// reconnect starts at seq=1; future spec adds Last-Event-ID replay).
type restEventStreamAdapter struct {
	d   *app.Dispatcher
	log *slog.Logger
}

// SubscribeStream implements rest.EventStream. Wraps the underlying
// channel + cancel from app.Dispatcher.SubscribeStream and runs a
// translation goroutine that marshals each event into the rest.Event
// shape. The goroutine exits when the source channel closes OR the
// caller invokes the returned cancel.
func (a *restEventStreamAdapter) SubscribeStream(filter []string, bufSize int) (<-chan rest.Event, func()) {
	src, cancelSrc := a.d.SubscribeStream(filter, bufSize)
	out := make(chan rest.Event, bufSize)
	stop := make(chan struct{})

	go func() {
		defer close(out)
		var seq int64
		for {
			select {
			case <-stop:
				return
			case evt, ok := <-src:
				if !ok {
					return
				}
				seq++
				data, err := json.Marshal(evt.Payload)
				if err != nil {
					a.log.Error("rest event marshal", "err", err, "type", evt.Type)
					continue
				}
				select {
				case out <- rest.Event{Seq: seq, Type: evt.Type, Data: data}:
				case <-stop:
					return
				}
			}
		}
	}()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(stop)
			cancelSrc()
		})
	}
	return out, cancel
}

// startRESTHTTP optionally starts the REST primary adapter when both
// WAD_REST_HTTP_ADDR and WAD_REST_TOKEN are set. Spec 110a — single-
// env-var bearer token mode. Multi-token sqlite-backed admin lands
// in 110c.
//
// Behaviour matrix:
//
//	WAD_REST_HTTP_ADDR unset                 → no listener
//	WAD_REST_HTTP_ADDR set, WAD_REST_TOKEN
//	  unset                                  → REFUSE TO START
//	                                            (fails closed; misconfigured
//	                                            deploys would otherwise expose
//	                                            an unauthenticated daemon)
//	both set                                 → REST adapter listening
//
// Returns a Shutdown func usable inside the existing signal-driven
// shutdown sequence (mirrors startHealthHTTP from spec 109).
//
// Spec 110b: the events arg is the spec 110b SSE stream source (the
// daemon's *app.Dispatcher). Pass nil to disable SSE — GET /v1/events
// then returns 503 with a JSON-RPC error envelope.
func startRESTHTTP(ctx context.Context, dispatcher rest.Dispatcher, events *app.Dispatcher, log *slog.Logger) (func(context.Context) error, error) {
	addr := os.Getenv("WAD_REST_HTTP_ADDR")
	if addr == "" {
		return func(context.Context) error { return nil }, nil
	}
	token := os.Getenv("WAD_REST_TOKEN")
	if token == "" {
		return nil, errors.New("WAD_REST_HTTP_ADDR is set but WAD_REST_TOKEN is empty; refusing to start REST adapter (would expose unauthenticated daemon)")
	}
	if err := rest.ValidateTokenStrength(token); err != nil {
		return nil, err
	}

	// Codex review PR 110a §HIGH: warn loudly when the listener is
	// bound to a public interface. The operator may legitimately want
	// 0.0.0.0 inside a Dokku container so nginx-buildpack can reach
	// the upstream — but they MUST be running behind a TLS-terminating
	// reverse proxy. We don't block (would defeat the Dokku case);
	// instead, the WAD_REST_TRUST_PROXY env must be set to acknowledge
	// the deploy posture.
	if err := warnIfPublicBind(addr, log); err != nil {
		return nil, err
	}

	auth := rest.NewEnvTokenAuth(token)
	opts := []rest.ServerOption{rest.WithLogger(log)}
	if events != nil {
		opts = append(opts, rest.WithEventStream(&restEventStreamAdapter{d: events, log: log}))
	}
	srv, err := rest.NewServer(ctx, addr, dispatcher, auth, opts...)
	if err != nil {
		return nil, fmt.Errorf("rest: %w", err)
	}
	go func() {
		if err := srv.Serve(); err != nil {
			log.Error("rest serve", "err", err)
		}
	}()
	log.Info("rest http listening", "addr", srv.ListenerAddr().String())

	return srv.Shutdown, nil
}

// warnIfPublicBind enforces the bind-policy gate. Returns nil for
// loopback binds. For non-loopback binds, requires WAD_REST_TRUST_PROXY=1
// as an explicit operator acknowledgement that a TLS-terminating
// reverse proxy sits in front of the daemon.
func warnIfPublicBind(addr string, log *slog.Logger) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// addr might be ":8080" with empty host — that binds all
		// interfaces. SplitHostPort returns an error for some
		// edge cases; treat any parse failure as "could be public".
		host = ""
	}
	if isLoopback(host) {
		return nil
	}
	if os.Getenv("WAD_REST_TRUST_PROXY") != "1" {
		return fmt.Errorf("WAD_REST_HTTP_ADDR=%q binds non-loopback (host=%q); set WAD_REST_TRUST_PROXY=1 to acknowledge that a TLS-terminating reverse proxy sits in front of the daemon", addr, host)
	}
	log.Warn("rest http bound to non-loopback; trusting WAD_REST_TRUST_PROXY=1 — confirm a TLS-terminating reverse proxy is in front", "addr", addr)
	return nil
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "", "0.0.0.0", "::":
		// Empty = all interfaces; 0.0.0.0 / :: = all IPv4/v6. NOT loopback.
		return false
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
