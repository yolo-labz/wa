package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// healthHTTPServer wraps an opt-in HTTP server that exposes /healthz
// (liveness) and /readyz (readiness) probes. Spec 109 — the daemon's
// primary protocol is the unix socket, but Dokku / k8s probes need
// HTTP. The endpoint is auth-free (probes do not carry credentials)
// and does NOT expose any application data — only liveness/readiness
// booleans.
//
// Activated when WAD_HEALTH_HTTP_ADDR is set (e.g. ":8080"). When
// unset, every helper here is a no-op so the daemon's failure modes
// stay byte-identical to the pre-spec-109 socket-only path.
type healthHTTPServer struct {
	srv      *http.Server
	listener net.Listener
}

// startHealthHTTP starts the health endpoint when WAD_HEALTH_HTTP_ADDR
// is set. ready is consulted on every /readyz request to flip 200/503
// — must be cheap (sub-millisecond) per the docker-container-healthchecker
// 5-second total budget. Returns a non-nil disabled server when the env
// var is unset so callers can pass the env-driven activation through
// unchanged. The disabled instance's Shutdown is a no-op.
func startHealthHTTP(ready func(context.Context) bool, log *slog.Logger) (*healthHTTPServer, error) {
	addr := os.Getenv("WAD_HEALTH_HTTP_ADDR")
	if addr == "" {
		return &healthHTTPServer{}, nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Liveness: process is alive and the listener is bound. If we
		// reached this handler, both are true. Always 200.
		// Liveness MUST NOT depend on whatsmeow connection state per
		// spec 109 §FR-004 — a logged-out daemon must keep its pod so
		// operators can re-pair via `wad pair`. Readiness is the
		// channel for "draining traffic", not liveness.
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// Readiness: paired AND connected. 503 while pairing or
		// disconnected so Dokku / k8s drain traffic.
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		if ready != nil && ready(ctx) {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintln(w, "ready")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintln(w, "not ready")
	})
	// Bind synchronously so a port-already-in-use error surfaces to
	// the caller before main proceeds — Codex review caught the goroutine-
	// only ListenAndServe pattern as silent-failure-prone.
	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("health http listen %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health http serve", "err", err, "addr", addr)
		}
	}()
	log.Info("health http listening", "addr", listener.Addr().String())
	return &healthHTTPServer{srv: srv, listener: listener}, nil
}

// Shutdown gracefully stops the health server. Safe to call on a nil
// receiver — matches the (nil, nil) return from startHealthHTTP when
// the env var is unset.
func (h *healthHTTPServer) Shutdown(ctx context.Context) error {
	if h == nil || h.srv == nil {
		return nil
	}
	return h.srv.Shutdown(ctx)
}
