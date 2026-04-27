package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Exporter enumerates the supported OTel exporters. Only two shipped
// in Tier 3: stdout (default, JSONL to wad.log) and otlp-unix (opt-in,
// gRPC over a unix socket). TCP OTLP is explicitly rejected per
// constitution IV.21 — wad never opens a network listener.
type Exporter string

const (
	// ExporterStdout writes span + metric JSONL to cfg.StdoutPath.
	ExporterStdout Exporter = "stdout"
	// ExporterOTLPUnix dials a gRPC OTLP receiver on cfg.OTLPSocket
	// (unix domain socket). Intended for a sidecar collector.
	ExporterOTLPUnix Exporter = "otlp-unix"
)

// Config captures the knobs Init needs. Populated from env vars by
// ConfigFromEnv; Init never reads env directly so tests can pass a
// struct literal and skip the env dance.
type Config struct {
	// Exporter picks the backend. Empty defaults to ExporterStdout.
	Exporter Exporter
	// Required mirrors WA_OTEL_REQUIRED — when true, a failed
	// exporter init aborts daemon startup instead of warning and
	// degrading to a no-op. FR-037 calls this "loud error".
	Required bool
	// StdoutPath is the JSONL destination for ExporterStdout.
	// Defaults to the daemon log path; tests point to a tmp file.
	StdoutPath string
	// OTLPSocket is the unix-domain socket path for ExporterOTLPUnix.
	// Required (and validated non-empty) when Exporter is otlp-unix.
	OTLPSocket string
	// Profile feeds the wa.profile resource attribute.
	Profile string
	// ServiceVersion is stamped into the resource; usually the wad
	// build version. Empty allowed — OTel will leave it unset.
	ServiceVersion string
	// Logger receives WARN / INFO breadcrumbs. Nil falls back to
	// slog.Default() so callers don't need to plumb one in tests.
	Logger *slog.Logger
}

// ConfigFromEnv reads the WA_OTEL_* environment variables and blends
// them with defaults. profile is the active wa profile; wadLogPath
// is the per-profile JSONL destination for the stdout exporter.
func ConfigFromEnv(profile, wadLogPath string) Config {
	exp := Exporter(os.Getenv("WA_OTEL_EXPORTER"))
	if exp == "" {
		exp = ExporterStdout
	}
	return Config{
		Exporter:   exp,
		Required:   os.Getenv("WA_OTEL_REQUIRED") == "1",
		StdoutPath: wadLogPath,
		OTLPSocket: os.Getenv("WA_OTEL_OTLP_SOCKET"),
		Profile:    profile,
	}
}

// Shutdown flushes and releases exporter resources. Safe to call
// multiple times; second and subsequent calls are no-ops.
type Shutdown func(context.Context) error

// Init wires a TracerProvider + MeterProvider from cfg and installs
// them as the otel global providers. Returns a Shutdown hook the
// daemon must defer.
//
// Failure modes:
//   - cfg.Required == true and exporter init fails → return error,
//     caller aborts startup (loud error per FR-037).
//   - cfg.Required == false and exporter init fails → log WARN,
//     install no-op providers, return (shutdown, nil). The daemon
//     continues running without telemetry rather than crashing.
//   - cfg.Exporter is unknown → always an error, regardless of
//     Required. Unknown exporter names are a config bug, not a
//     runtime degradation.
//
// "Never silent-fallback" per constitution §III.12: every degraded
// path emits a WARN log describing exactly which exporter failed and
// why. There is no path that returns nil error while silently
// skipping telemetry.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	switch cfg.Exporter {
	case ExporterStdout, "":
		return initStdout(ctx, cfg, res, log)
	case ExporterOTLPUnix:
		return initOTLPUnix(ctx, cfg, res, log)
	default:
		return nil, fmt.Errorf("observability: unknown WA_OTEL_EXPORTER %q (want stdout|otlp-unix)", cfg.Exporter)
	}
}

func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName("wad"),
		attribute.String("wa.profile", cfg.Profile),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	return resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithProcess(),
		resource.WithHost(),
	)
}

// initStdout opens cfg.StdoutPath (creating parents as needed), wires
// stdout{trace,metric} exporters onto that writer, and installs the
// providers globally.
func initStdout(ctx context.Context, cfg Config, res *resource.Resource, log *slog.Logger) (Shutdown, error) {
	path := cfg.StdoutPath
	if path == "" {
		return degrade(cfg, log, "stdout path empty", errors.New("WA_OTEL stdout exporter requires a log path"))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return degrade(cfg, log, "stdout parent dir", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // path comes from per-profile resolver, not user input
	if err != nil {
		return degrade(cfg, log, "stdout open", err)
	}

	traceExp, err := stdouttrace.New(stdouttrace.WithWriter(f))
	if err != nil {
		_ = f.Close()
		return degrade(cfg, log, "stdouttrace", err)
	}
	metricExp, err := stdoutmetric.New(stdoutmetric.WithWriter(f))
	if err != nil {
		_ = traceExp.Shutdown(ctx)
		_ = f.Close()
		return degrade(cfg, log, "stdoutmetric", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(traceExp),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(30*time.Second))),
	)
	installGlobals(tp, mp)
	log.Info("observability initialised", "exporter", "stdout", "path", path, "profile", cfg.Profile)

	return makeShutdown(tp, mp, f), nil
}

// initOTLPUnix dials the unix socket at cfg.OTLPSocket with insecure
// gRPC (the socket file mode is the only auth surface) and wires
// OTLP trace + metric exporters onto it.
func initOTLPUnix(ctx context.Context, cfg Config, res *resource.Resource, log *slog.Logger) (Shutdown, error) {
	sock := cfg.OTLPSocket
	if sock == "" {
		return degrade(cfg, log, "otlp-unix socket empty", errors.New("WA_OTEL_OTLP_SOCKET must be set for otlp-unix exporter"))
	}
	// Fail fast when the socket file is missing — dialing would
	// otherwise block on reconnect forever.
	if _, err := os.Stat(sock); err != nil {
		return degrade(cfg, log, "otlp-unix stat", err)
	}
	dialer := func(ctx context.Context, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", addr)
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	}

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(sock),
		otlptracegrpc.WithDialOption(dialOpts...),
	)
	if err != nil {
		return degrade(cfg, log, "otlptracegrpc", err)
	}
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(sock),
		otlpmetricgrpc.WithDialOption(dialOpts...),
	)
	if err != nil {
		_ = traceExp.Shutdown(ctx)
		return degrade(cfg, log, "otlpmetricgrpc", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(traceExp),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(30*time.Second))),
	)
	installGlobals(tp, mp)
	log.Info("observability initialised", "exporter", "otlp-unix", "socket", sock, "profile", cfg.Profile)

	return makeShutdown(tp, mp, nil), nil
}

// degrade is the single choke point for the Required flag. Every
// exporter init error funnels through here so the WARN-vs-fatal
// decision is centralised and audit-visible.
func degrade(cfg Config, log *slog.Logger, stage string, cause error) (Shutdown, error) {
	if cfg.Required {
		return nil, fmt.Errorf("observability: WA_OTEL_REQUIRED=1 and %s init failed: %w", stage, cause)
	}
	log.Warn("observability exporter degraded — continuing without telemetry (set WA_OTEL_REQUIRED=1 to refuse startup)",
		"stage", stage, "exporter", cfg.Exporter, "err", cause)
	// No-op shutdown; the otel globals remain the library default
	// (no-op) because we never installed anything.
	return func(context.Context) error { return nil }, nil
}

func installGlobals(tp *sdktrace.TracerProvider, mp *sdkmetric.MeterProvider) {
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
}

// makeShutdown returns a Shutdown hook idempotent across multiple
// calls. Flushes trace first (spans queued behind metric ticks are
// cheaper to lose), then metrics, then closes the writer if any.
func makeShutdown(tp *sdktrace.TracerProvider, mp *sdkmetric.MeterProvider, closer io.Closer) Shutdown {
	var once sync.Once
	return func(ctx context.Context) error {
		var finalErr error
		once.Do(func() {
			if err := tp.Shutdown(ctx); err != nil {
				finalErr = errors.Join(finalErr, fmt.Errorf("trace: %w", err))
			}
			if err := mp.Shutdown(ctx); err != nil {
				finalErr = errors.Join(finalErr, fmt.Errorf("metric: %w", err))
			}
			if closer != nil {
				if err := closer.Close(); err != nil {
					finalErr = errors.Join(finalErr, fmt.Errorf("writer: %w", err))
				}
			}
		})
		return finalErr
	}
}
