package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// pickFreePort returns a localhost address whose port is currently
// free. Used by health-http tests so two parallel tests do not
// collide on a hard-coded port.
func pickFreePort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func discardLogger() *slog.Logger {
	// slog.DiscardHandler available since Go 1.26 — preferred per
	// sloglint over passing io.Discard to NewTextHandler.
	_ = io.Discard
	return slog.New(slog.DiscardHandler)
}

// httpGetCtx is a noctx-friendly helper that issues a GET with a
// short context deadline and returns the response. Centralises the
// repeated pattern in health-http tests so no individual call site
// needs a //nolint:noctx directive.
func httpGetCtx(t *testing.T, url string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// TestHealthHTTP_NoEnvDisabled pins spec 109 §FR-004 fallback: when
// WAD_HEALTH_HTTP_ADDR is unset, startHealthHTTP returns a disabled
// server whose Shutdown is a no-op — the daemon's protocol surface
// stays unix-socket-only.
func TestHealthHTTP_NoEnvDisabled(t *testing.T) {
	t.Setenv("WAD_HEALTH_HTTP_ADDR", "")
	srv, err := startHealthHTTP(func(context.Context) bool { return true }, discardLogger())
	if err != nil {
		t.Fatalf("startHealthHTTP: %v", err)
	}
	if srv == nil {
		t.Fatal("disabled server should still return a non-nil receiver for safe Shutdown")
	}
	if srv.srv != nil {
		t.Errorf("disabled server has a wrapped *http.Server; want nil inner")
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Errorf("disabled Shutdown: %v", err)
	}
}

// TestHealthHTTP_Liveness pins spec 109 §FR-004: /healthz is always
// 200 once the listener is bound. Liveness MUST NOT depend on
// whatsmeow connection state — a logged-out daemon must keep its pod
// alive so the operator can re-pair.
func TestHealthHTTP_Liveness(t *testing.T) {
	addr := pickFreePort(t)
	t.Setenv("WAD_HEALTH_HTTP_ADDR", addr)
	srv, err := startHealthHTTP(func(context.Context) bool { return false }, discardLogger())
	if err != nil {
		t.Fatalf("startHealthHTTP: %v", err)
	}
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	})

	waitListener(t, addr, 2*time.Second)

	resp := httpGetCtx(t, "http://"+addr+"/healthz")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200 (liveness MUST NOT depend on ready state)", resp.StatusCode)
	}
}

// TestHealthHTTP_ReadinessFlips pins spec 109 §FR-004: /readyz
// returns 503 when ready() reports false (pairing/disconnected) and
// 200 when ready() reports true. Dokku drains traffic on 503.
func TestHealthHTTP_ReadinessFlips(t *testing.T) {
	addr := pickFreePort(t)
	t.Setenv("WAD_HEALTH_HTTP_ADDR", addr)
	var ready bool
	srv, err := startHealthHTTP(func(context.Context) bool { return ready }, discardLogger())
	if err != nil {
		t.Fatalf("startHealthHTTP: %v", err)
	}
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	})

	waitListener(t, addr, 2*time.Second)

	resp1 := httpGetCtx(t, "http://"+addr+"/readyz")
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz when not ready = %d, want 503", resp1.StatusCode)
	}

	ready = true
	resp2 := httpGetCtx(t, "http://"+addr+"/readyz")
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/readyz when ready = %d, want 200", resp2.StatusCode)
	}
}

// TestHealthHTTP_ShutdownGraceful pins that Shutdown() returns within
// the deadline and the listener stops accepting new connections.
// Critical for Dokku's DOKKU_DOCKER_STOP_TIMEOUT contract.
func TestHealthHTTP_ShutdownGraceful(t *testing.T) {
	addr := pickFreePort(t)
	t.Setenv("WAD_HEALTH_HTTP_ADDR", addr)
	srv, err := startHealthHTTP(func(context.Context) bool { return true }, discardLogger())
	if err != nil {
		t.Fatalf("startHealthHTTP: %v", err)
	}
	waitListener(t, addr, 2*time.Second)

	shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer dialCancel()
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err == nil {
		_ = conn.Close()
		t.Error("listener accepted new conn after Shutdown")
	}
}

// waitListener polls until the address accepts a TCP connection or
// the deadline expires.
func waitListener(t *testing.T, addr string, total time.Duration) {
	t.Helper()
	dialer := &net.Dialer{Timeout: 50 * time.Millisecond}
	deadline := time.Now().Add(total)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return
		}
		if errors.Is(err, os.ErrPermission) {
			t.Fatalf("waitListener: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitListener: %s never bound within %v", addr, total)
}
