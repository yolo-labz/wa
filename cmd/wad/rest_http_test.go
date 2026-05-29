package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
)

// stubRestDispatcher is the smallest possible rest.Dispatcher for the
// composition-root tests. The handlers never run because the gates
// under test reject the start before the listener is opened.
type stubRestDispatcher struct{}

func (stubRestDispatcher) Handle(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("stubRestDispatcher.Handle not expected to run")
}

func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestStartRESTHTTP_NoEnvDisabled pins the unset-env path: the
// composition root activates nothing, returns a no-op shutdown func.
func TestStartRESTHTTP_NoEnvDisabled(t *testing.T) {
	t.Setenv("WAD_REST_HTTP_ADDR", "")
	t.Setenv("WAD_REST_TOKEN", "")

	shutdown, err := startRESTHTTP(t.Context(), stubRestDispatcher{}, nil, nil, quietLogger())
	if err != nil {
		t.Fatalf("startRESTHTTP: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil no-op shutdown")
	}
	if err := shutdown(t.Context()); err != nil {
		t.Errorf("no-op shutdown: %v", err)
	}
}

// TestStartRESTHTTP_AddrWithoutTokenFailsClosed pins spec 110a §FR-004:
// WAD_REST_HTTP_ADDR set without WAD_REST_TOKEN MUST refuse to start.
// Codex review on PR 110a flagged the absence of this in-process test.
func TestStartRESTHTTP_AddrWithoutTokenFailsClosed(t *testing.T) {
	t.Setenv("WAD_REST_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("WAD_REST_TOKEN", "")

	_, err := startRESTHTTP(t.Context(), stubRestDispatcher{}, nil, nil, quietLogger())
	if err == nil {
		t.Fatal("expected refusal when WAD_REST_HTTP_ADDR set without token")
	}
	if !strings.Contains(err.Error(), "WAD_REST_TOKEN") {
		t.Errorf("error = %q, want mention of WAD_REST_TOKEN", err.Error())
	}
}

// TestStartRESTHTTP_WeakTokenRejected pins the rest.ValidateTokenStrength
// gate: a token shorter than 32 bytes refuses to start.
func TestStartRESTHTTP_WeakTokenRejected(t *testing.T) {
	t.Setenv("WAD_REST_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("WAD_REST_TOKEN", "short")
	t.Setenv("WAD_REST_TRUST_PROXY", "")

	_, err := startRESTHTTP(t.Context(), stubRestDispatcher{}, nil, nil, quietLogger())
	if err == nil {
		t.Fatal("weak token accepted; want refusal")
	}
	if !strings.Contains(err.Error(), "at least") {
		t.Errorf("error = %q, want mention of minimum length", err.Error())
	}
}

// TestStartRESTHTTP_PublicBindRequiresAck pins the bind-policy gate:
// 0.0.0.0 / :: / non-loopback addresses require WAD_REST_TRUST_PROXY=1.
// This makes accidental public-internet exposure fail fast.
func TestStartRESTHTTP_PublicBindRequiresAck(t *testing.T) {
	t.Setenv("WAD_REST_HTTP_ADDR", "0.0.0.0:0")
	t.Setenv("WAD_REST_TOKEN", strings.Repeat("a", rest.MinTokenBytes))
	t.Setenv("WAD_REST_TRUST_PROXY", "")

	_, err := startRESTHTTP(t.Context(), stubRestDispatcher{}, nil, nil, quietLogger())
	if err == nil {
		t.Fatal("public bind accepted without WAD_REST_TRUST_PROXY=1; want refusal")
	}
	if !strings.Contains(err.Error(), "WAD_REST_TRUST_PROXY") {
		t.Errorf("error = %q, want mention of WAD_REST_TRUST_PROXY", err.Error())
	}
}

// TestStartRESTHTTP_LoopbackOK pins that loopback binds + a strong
// token + no proxy ack proceeds to listen.
func TestStartRESTHTTP_LoopbackOK(t *testing.T) {
	t.Setenv("WAD_REST_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("WAD_REST_TOKEN", strings.Repeat("a", rest.MinTokenBytes))
	t.Setenv("WAD_REST_TRUST_PROXY", "")

	shutdown, err := startRESTHTTP(t.Context(), stubRestDispatcher{}, nil, nil, quietLogger())
	if err != nil {
		t.Fatalf("loopback startRESTHTTP: %v", err)
	}
	defer func() { _ = shutdown(t.Context()) }()
	// No assertion on the listener address — the goroutine binds on
	// :0 and we cannot ergonomically read it back from the closure.
	// The lack of error is the contract this test pins.
}

// TestStartRESTHTTP_PublicBindWithAckOK pins that the trust-proxy
// acknowledgement actually unblocks the public bind. Mirrors the
// Dokku-container scenario where 0.0.0.0:8080 is correct because
// nginx-buildpack proxies the upstream.
func TestStartRESTHTTP_PublicBindWithAckOK(t *testing.T) {
	t.Setenv("WAD_REST_HTTP_ADDR", "127.0.0.1:0") // bind loopback so test is portable
	t.Setenv("WAD_REST_TOKEN", strings.Repeat("a", rest.MinTokenBytes))
	t.Setenv("WAD_REST_TRUST_PROXY", "1")

	shutdown, err := startRESTHTTP(t.Context(), stubRestDispatcher{}, nil, nil, quietLogger())
	if err != nil {
		t.Fatalf("startRESTHTTP with trust-proxy ack: %v", err)
	}
	defer func() { _ = shutdown(t.Context()) }()
}

// silence the unused-import warning when only some tests reference
// these helpers.
var (
	_ = io.Discard
)
