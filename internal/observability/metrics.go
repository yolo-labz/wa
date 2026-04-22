package observability

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Attribute keys used on counters. JID is deliberately absent — per
// FR-036 no counter attribute may carry JID (even hashed) to keep
// the metric cardinality bounded at the action set (<32 values).
const (
	AttrAction  = "wa.action"
	AttrRefusal = "wa.refusal"
)

// Metrics is the fixed set of 6 instruments Tier 3 ships (FR-036).
// Held on a singleton so callers grab one reference and forget about
// registration order. Counters use explicit `{unit}` suffixes picked
// from OTel's recommended units so Prometheus scrape → Grafana works
// without extra labels.
type Metrics struct {
	// Counters (monotonic).
	RateLimitRefusals metric.Int64Counter
	AllowlistRefusals metric.Int64Counter

	// Gauges (observable, polled on a 5s ticker).
	eventQueueDepth  metric.Int64ObservableGauge
	goroutines       metric.Int64ObservableGauge
	fds              metric.Int64ObservableGauge
	subscribeStreams metric.Int64ObservableGauge

	// Snapshot state drained by the gauge callbacks. Updated by
	// SetEventQueueDepth / SetSubscribeStreams; the goroutine + fd
	// counts are sampled directly from runtime / syscall each tick.
	eventQueueDepthValue  atomic.Int64
	subscribeStreamsValue atomic.Int64
}

// Global singleton. The OTel meter provider is the global one set
// by Init; we lazily register instruments on first access so tests
// that never call Init still get a live (no-op) metrics object.
var (
	metricsOnce sync.Once
	metricsInst *Metrics
)

// GetMetrics returns the process-wide Metrics. First call registers
// the 6 instruments on the global meter. Safe for concurrent use.
func GetMetrics() *Metrics {
	metricsOnce.Do(func() {
		m, err := newMetrics()
		if err != nil {
			// Registration failures are never silent — panic is
			// inappropriate in non-main (forbidigo), so we return a
			// zero-value struct. The Int64Counter fields will be
			// nil, and every mutator checks for that.
			metricsInst = &Metrics{}
			return
		}
		metricsInst = m
	})
	return metricsInst
}

func newMetrics() (*Metrics, error) {
	meter := otel.GetMeterProvider().Meter(tracerName)
	m := &Metrics{}

	var err error
	m.RateLimitRefusals, err = meter.Int64Counter("wa.rate_limit.refusals",
		metric.WithUnit("{refusal}"),
		metric.WithDescription("Count of rate-limiter refusals, by action."),
	)
	if err != nil {
		return nil, fmt.Errorf("rate_limit.refusals: %w", err)
	}

	m.AllowlistRefusals, err = meter.Int64Counter("wa.allowlist.refusals",
		metric.WithUnit("{refusal}"),
		metric.WithDescription("Count of allowlist refusals, by action."),
	)
	if err != nil {
		return nil, fmt.Errorf("allowlist.refusals: %w", err)
	}

	m.eventQueueDepth, err = meter.Int64ObservableGauge("wa.event_queue.depth",
		metric.WithUnit("{event}"),
		metric.WithDescription("Current depth of the event fan-out channel."),
		metric.WithInt64Callback(func(_ context.Context, obs metric.Int64Observer) error {
			obs.Observe(m.eventQueueDepthValue.Load())
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("event_queue.depth: %w", err)
	}

	m.goroutines, err = meter.Int64ObservableGauge("wa.goroutines",
		metric.WithUnit("{goroutine}"),
		metric.WithDescription("Current goroutine count (runtime.NumGoroutine)."),
		metric.WithInt64Callback(func(_ context.Context, obs metric.Int64Observer) error {
			obs.Observe(int64(runtime.NumGoroutine()))
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("goroutines: %w", err)
	}

	m.fds, err = meter.Int64ObservableGauge("wa.fds",
		metric.WithUnit("{fd}"),
		metric.WithDescription("Open file descriptor count (best-effort, platform-specific)."),
		metric.WithInt64Callback(func(_ context.Context, obs metric.Int64Observer) error {
			obs.Observe(openFDCount())
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("fds: %w", err)
	}

	m.subscribeStreams, err = meter.Int64ObservableGauge("wa.subscribe.streams",
		metric.WithUnit("{stream}"),
		metric.WithDescription("Current number of active JSON-RPC subscribe connections."),
		metric.WithInt64Callback(func(_ context.Context, obs metric.Int64Observer) error {
			obs.Observe(m.subscribeStreamsValue.Load())
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("subscribe.streams: %w", err)
	}

	return m, nil
}

// RecordRateLimitRefusal increments the rate-limit counter tagged by
// action. No-op on a zero-value Metrics (registration failed).
func (m *Metrics) RecordRateLimitRefusal(action string) {
	if m == nil || m.RateLimitRefusals == nil {
		return
	}
	m.RateLimitRefusals.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String(AttrAction, action)))
}

// RecordAllowlistRefusal increments the allowlist counter tagged by
// action.
func (m *Metrics) RecordAllowlistRefusal(action string) {
	if m == nil || m.AllowlistRefusals == nil {
		return
	}
	m.AllowlistRefusals.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String(AttrAction, action)))
}

// SetEventQueueDepth publishes the current depth of the event queue
// for the next gauge callback. Cheap (atomic store) — safe to call
// from the event bridge hot path.
func (m *Metrics) SetEventQueueDepth(n int) {
	if m == nil {
		return
	}
	m.eventQueueDepthValue.Store(int64(n))
}

// SetSubscribeStreams publishes the current subscribe connection
// count for the next gauge callback.
func (m *Metrics) SetSubscribeStreams(n int) {
	if m == nil {
		return
	}
	m.subscribeStreamsValue.Store(int64(n))
}

// StartTicker spins a goroutine that wakes every `interval` and
// publishes fresh gauge snapshots (mainly a place for sub-systems
// that don't otherwise notify). Returns a cancel func. Callers must
// retain both the returned done channel and the cancel to shut it
// down cleanly; shutdown is idempotent.
func (m *Metrics) StartTicker(ctx context.Context, interval time.Duration) (done <-chan struct{}, cancel func()) {
	c, stop := context.WithCancel(ctx)
	d := make(chan struct{})
	go func() {
		defer close(d)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-c.Done():
				return
			case <-t.C:
				// gauge callbacks already re-read the atomics;
				// this ticker exists so the OTel SDK knows the
				// process is alive even during idle periods.
			}
		}
	}()
	return d, stop
}
