package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
)

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
func startRESTHTTP(ctx context.Context, dispatcher rest.Dispatcher, log *slog.Logger) (func(context.Context) error, error) {
	addr := os.Getenv("WAD_REST_HTTP_ADDR")
	if addr == "" {
		return func(context.Context) error { return nil }, nil
	}
	token := os.Getenv("WAD_REST_TOKEN")
	if token == "" {
		return nil, errors.New("WAD_REST_HTTP_ADDR is set but WAD_REST_TOKEN is empty; refusing to start REST adapter (would expose unauthenticated daemon)")
	}

	auth := rest.NewEnvTokenAuth(token)
	srv, err := rest.NewServer(ctx, addr, dispatcher, auth, rest.WithLogger(log))
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
