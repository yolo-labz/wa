// Package testscript holds black-box end-to-end tests that exercise the
// whole `wad` wiring through the unix socket. Each file in this package
// runs as an isolated test binary (Go builds one binary per package),
// which is the sole mechanism we have for resetting OTel's process-wide
// singletons — `metricsOnce`, `otel.SetTracerProvider`, etc. Do NOT fold
// these tests into `cmd/wad` or `internal/observability`; cross-test
// singleton state there would make the assertions non-deterministic.
package testscript

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket/sockettest"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// TestSpanAndMetricWithin10s is the T3-21 observability e2e gate
// (`specs/018-parity-hardening/tasks-tier3.md` T3-21, SC-008). It
// spins `wad`'s in-process wiring under a hermetic profile, routes one
// `send` JSON-RPC call through the socket to the memory adapter, and
// asserts that within 10 s the stdout OTel exporter has written BOTH
// a `wa.send` span AND at least one of the `wa.rate_limit.refusals`
// / `wa.event_queue.depth` metrics to `wad.log`.
//
// Why this shape:
//   - Process isolation via the `testscript` package boundary resets
//     the OTel singletons between the unit tests and this e2e.
//   - `observability.Init` must run BEFORE any `GetMetrics()` caller
//     (the `sync.Once` inside `metrics.go` captures whichever meter
//     provider is global at first call).
//   - Shutdown is the forcing function: `mp.Shutdown` calls
//     `ForceFlush` internally, so the default 30 s periodic reader
//     interval does not gate this test — one clean shutdown emits
//     the current gauge values immediately.
func TestSpanAndMetricWithin10s(t *testing.T) {
	t.Parallel()

	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("total wall time %s exceeded 10 s budget", elapsed)
		}
	}()

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "wad.log")

	// Hermetic OTel boot: stdout exporter → per-test JSONL file,
	// Required=true so a degraded/no-op initialisation fails the
	// test loudly (constitution §III.12) instead of producing a
	// misleading green pass on zero output.
	shutdownOtel, err := observability.Init(context.Background(), observability.Config{
		Exporter:   observability.ExporterStdout,
		Required:   true,
		StdoutPath: logPath,
		Profile:    "testscript",
	})
	if err != nil {
		t.Fatalf("observability.Init: %v", err)
	}
	// Pre-touch the metrics singleton NOW, while OUR TracerProvider +
	// MeterProvider are the globals. If any other goroutine hits
	// GetMetrics first under a different provider, the instruments
	// silently bind to the wrong meter and this test would never see
	// the metric.
	_ = observability.GetMetrics()

	log := slog.New(slog.DiscardHandler)

	mem := memory.New(nil)
	// Memory allowlist defaults to deny-all; grant the test recipient
	// so the send reaches the sender (and so the span records success,
	// not a denied-allowlist short-circuit — the refusal metric is
	// driven by the rate limiter instead, which is what we assert on).
	jid, err := domain.Parse("5511987654321@s.whatsapp.net")
	if err != nil {
		t.Fatalf("domain.Parse: %v", err)
	}
	mem.Grant(jid, domain.ActionSend, domain.ActionRead)

	dispatcher := app.NewDispatcher(app.DispatcherConfig{
		Sender:         mem,
		Events:         mem,
		Contacts:       mem,
		Groups:         mem,
		Session:        mem,
		Allowlist:      mem,
		Audit:          mem,
		History:        mem,
		Pairer:         mem,
		Profile:        "testscript",
		SessionCreated: time.Now(),
		Logger:         log,
	})
	t.Cleanup(func() { _ = dispatcher.Close() })

	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	da := newTestDispatcherAdapter(bridgeCtx, dispatcher)
	t.Cleanup(func() {
		bridgeCancel()
		da.Close()
	})

	sockPath := filepath.Join(tmp, "wa.sock")
	server := socket.NewServer(da, log, socket.WithShutdownDeadline(2*time.Second))

	srvCtx, srvCancel := context.WithCancel(context.Background())
	srvDone := make(chan error, 1)
	go func() { srvDone <- server.Run(srvCtx, sockPath) }()

	sockettest.WaitForSocket(t, sockPath, 3*time.Second)

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", sockPath)
	if err != nil {
		srvCancel()
		t.Fatalf("dial: %v", err)
	}
	scanner := sockettest.HandshakeHello(t, conn)

	const req = `{"jsonrpc":"2.0","id":1,"method":"send","params":{"to":"5511987654321@s.whatsapp.net","body":"hello"}}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		srvCancel()
		t.Fatalf("write send: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if !scanner.Scan() {
		_ = conn.Close()
		srvCancel()
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Fatal("send: connection closed before response")
	}
	// Parse the response so a handler-level error surfaces as a test
	// failure instead of being masked by a successful log parse.
	var resp struct {
		Result json.RawMessage  `json:"result"`
		Error  *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		_ = conn.Close()
		srvCancel()
		t.Fatalf("unmarshal send response: %v (raw: %s)", err, scanner.Bytes())
	}
	_ = conn.Close()
	if resp.Error != nil {
		srvCancel()
		t.Fatalf("send returned JSON-RPC error: %s", *resp.Error)
	}

	// Shut the server and OTel down — OTel's BatchSpanProcessor and
	// PeriodicReader both flush on Shutdown, so this is the forcing
	// function that makes the JSONL observable without waiting on the
	// 30 s metric tick.
	srvCancel()
	select {
	case err := <-srvDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("server.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop within 5 s")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	if err := shutdownOtel(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("observability.Shutdown: %v", err)
	}
	cancelShutdown()

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read %s: %v", logPath, err)
	}
	if len(raw) == 0 {
		t.Fatalf("%s is empty — the exporter never flushed", logPath)
	}

	text := string(raw)
	// The stdouttrace encoder uses Go-struct JSON field names (`Name`
	// for span name). Accept both capitalisations so a future SDK
	// release that switches to lower-case tags does not flap this
	// test without a real regression.
	haveSpan := strings.Contains(text, `"Name":"wa.send"`) ||
		strings.Contains(text, `"name":"wa.send"`)
	haveMetric := strings.Contains(text, "wa.rate_limit.refusals") ||
		strings.Contains(text, "wa.event_queue.depth")

	if !haveSpan {
		t.Errorf("expected a wa.send span in %s; first 2 KB:\n%s",
			logPath, firstN(text, 2048))
	}
	if !haveMetric {
		t.Errorf("expected wa.rate_limit.refusals OR wa.event_queue.depth metric in %s; first 2 KB:\n%s",
			logPath, firstN(text, 2048))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// testDispatcherAdapter is a local copy of cmd/wad/adapter.go's
// `dispatcherAdapter`. cmd/wad is package main and not importable; this
// duplication is small (~30 lines) and the two MUST stay behaviourally
// identical — if you change the fan-out shape in cmd/wad, mirror it here
// or the e2e surface diverges from production.
type testDispatcherAdapter struct {
	d      *app.Dispatcher
	events chan socket.Event
	done   chan struct{}
}

func newTestDispatcherAdapter(ctx context.Context, d *app.Dispatcher) *testDispatcherAdapter {
	da := &testDispatcherAdapter{
		d:      d,
		events: make(chan socket.Event, 64),
		done:   make(chan struct{}),
	}
	go da.run(ctx)
	return da
}

func (da *testDispatcherAdapter) run(ctx context.Context) {
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
			select {
			case da.events <- socket.Event{Type: ae.Type, Payload: ae.Payload}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (da *testDispatcherAdapter) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return da.d.Handle(ctx, method, params)
}

func (da *testDispatcherAdapter) Events() <-chan socket.Event {
	return da.events
}

func (da *testDispatcherAdapter) Close() { <-da.done }

// Compile-time check that testDispatcherAdapter satisfies socket.Dispatcher.
var _ socket.Dispatcher = (*testDispatcherAdapter)(nil)
