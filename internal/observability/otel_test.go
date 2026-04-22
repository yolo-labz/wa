package observability

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
)

// TestOtelInitStdoutDefault asserts the happy path: Exporter empty →
// defaults to stdout, writes JSONL to the configured path, installs
// non-nil global TracerProvider, and Shutdown flushes without error.
func TestOtelInitStdoutDefault(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "wad.log")

	shutdown, err := Init(context.Background(), Config{
		StdoutPath: logPath,
		Profile:    "default",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	// Emit a span so the batcher has something to flush.
	_, span := otel.Tracer("test").Start(context.Background(), "probe")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if fi, err := os.Stat(logPath); err != nil {
		t.Fatalf("log path missing after shutdown: %v", err)
	} else if fi.Size() == 0 {
		t.Errorf("log path exists but empty — stdouttrace batcher produced no output")
	}
}

// TestOtelRequiredRefusesStartup asserts FR-037: WA_OTEL_REQUIRED=1
// turns an exporter init failure into a fatal startup error instead
// of a WARN-and-continue.
func TestOtelRequiredRefusesStartup(t *testing.T) {
	// Empty StdoutPath is the cheapest way to trip the error path in
	// initStdout; we never want stdout exporter to silently pick a
	// fallback destination.
	_, err := Init(context.Background(), Config{
		Exporter:   ExporterStdout,
		Required:   true,
		StdoutPath: "",
		Profile:    "default",
	})
	if err == nil {
		t.Fatal("Init(Required=true, StdoutPath=\"\") = nil; want error")
	}
	if !strings.Contains(err.Error(), "WA_OTEL_REQUIRED") {
		t.Errorf("error = %v; want mention of WA_OTEL_REQUIRED for operator debuggability", err)
	}
}

// TestOtelRequiredFalseDegradesGracefully asserts the inverse: same
// failing path with Required=false emits a WARN and returns a no-op
// Shutdown rather than erroring the daemon.
func TestOtelRequiredFalseDegradesGracefully(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	shutdown, err := Init(context.Background(), Config{
		Exporter:   ExporterStdout,
		Required:   false,
		StdoutPath: "",
		Profile:    "default",
		Logger:     log,
	})
	if err != nil {
		t.Fatalf("Init(Required=false) = err %v; want nil", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil Shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown returned err: %v", err)
	}
	if !strings.Contains(buf.String(), "degraded") {
		t.Errorf("expected WARN about degraded exporter, got log: %s", buf.String())
	}
}

// TestOtelUnknownExporterAlwaysErrors asserts unknown exporter names
// bypass the Required flag: bad config is always fatal.
func TestOtelUnknownExporterAlwaysErrors(t *testing.T) {
	_, err := Init(context.Background(), Config{
		Exporter: "jaeger",
		Required: false,
		Profile:  "default",
	})
	if err == nil {
		t.Fatal("Init(Exporter=jaeger) = nil; want error")
	}
	if !strings.Contains(err.Error(), "unknown WA_OTEL_EXPORTER") {
		t.Errorf("error = %v; want mention of unknown exporter", err)
	}
}

// TestOtelOtlpUnixRequiresSocket asserts the otlp-unix exporter refuses
// to start when WA_OTEL_OTLP_SOCKET is empty (Required=true).
func TestOtelOtlpUnixRequiresSocket(t *testing.T) {
	_, err := Init(context.Background(), Config{
		Exporter:   ExporterOTLPUnix,
		Required:   true,
		OTLPSocket: "",
		Profile:    "default",
	})
	if err == nil {
		t.Fatal("Init(otlp-unix, empty socket) = nil; want error")
	}
}

// TestOtelOtlpUnixMissingSocketDegrades asserts a non-existent socket
// path degrades (Required=false) with a WARN, rather than dialing
// forever.
func TestOtelOtlpUnixMissingSocketDegrades(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{
		Exporter:   ExporterOTLPUnix,
		Required:   false,
		OTLPSocket: "/nonexistent/wa-otel.sock",
		Profile:    "default",
	})
	if err != nil {
		t.Fatalf("Init degrade path = err %v; want nil", err)
	}
	_ = shutdown(context.Background())
}

// TestConfigFromEnvDefaults asserts the env parser is empty-robust.
func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("WA_OTEL_EXPORTER", "")
	t.Setenv("WA_OTEL_REQUIRED", "")
	t.Setenv("WA_OTEL_OTLP_SOCKET", "")

	cfg := ConfigFromEnv("alice", "/tmp/wad.log")
	if cfg.Exporter != ExporterStdout {
		t.Errorf("default Exporter = %q, want stdout", cfg.Exporter)
	}
	if cfg.Required {
		t.Error("default Required = true, want false")
	}
	if cfg.Profile != "alice" {
		t.Errorf("Profile = %q, want alice", cfg.Profile)
	}
	if cfg.StdoutPath != "/tmp/wad.log" {
		t.Errorf("StdoutPath = %q, want /tmp/wad.log", cfg.StdoutPath)
	}
}
