package observability

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestMetricsRegistered6 asserts all 6 instruments Tier 3 ships
// register with the expected names. Bumping to 7 metrics MUST update
// this test — it's the signal that someone moved the cardinality
// ceiling without thinking.
func TestMetricsRegistered6(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	// Reset the singleton; we need the 6 instruments to register
	// against the manual-reader provider we just installed.
	metricsOnce = sync.Once{}
	metricsInst = nil

	m := GetMetrics()
	// Drive the counters so the Collect call below sees them.
	m.RecordRateLimitRefusal("send")
	m.RecordAllowlistRefusal("send")
	m.SetEventQueueDepth(4)
	m.SetSubscribeStreams(1)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("reader.Collect: %v", err)
	}
	got := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			got[mm.Name] = true
		}
	}
	for _, want := range []string{
		"wa.rate_limit.refusals",
		"wa.allowlist.refusals",
		"wa.event_queue.depth",
		"wa.goroutines",
		"wa.fds",
		"wa.subscribe.streams",
	} {
		if !got[want] {
			t.Errorf("instrument %q not registered. Registered=%v", want, keys(got))
		}
	}
	if len(got) != 6 {
		t.Errorf("registered %d instruments, want exactly 6 (got %v)", len(got), keys(got))
	}
}

// TestNoJIDOnCounters asserts FR-036's cardinality rule: the refusal
// counters carry `wa.action` only. Adding `wa.jid` would explode the
// series count.
func TestNoJIDOnCounters(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	metricsOnce = sync.Once{}
	metricsInst = nil

	m := GetMetrics()
	m.RecordRateLimitRefusal("send")
	m.RecordAllowlistRefusal("react")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			if mm.Name != "wa.rate_limit.refusals" && mm.Name != "wa.allowlist.refusals" {
				continue
			}
			sum, ok := mm.Data.(metricdata.Sum[int64])
			if !ok {
				t.Errorf("%s: unexpected data type %T", mm.Name, mm.Data)
				continue
			}
			for _, dp := range sum.DataPoints {
				iter := dp.Attributes.Iter()
				for iter.Next() {
					a := iter.Attribute()
					if string(a.Key) == AttrJID {
						t.Errorf("%s data point carries wa.jid attribute — FR-036 violation", mm.Name)
					}
				}
			}
		}
	}
}

// TestCounterUnitSuffixStable pins the unit strings. Changing these
// requires a version bump in any backend dashboard referencing
// wa.rate_limit.refusals_refusal etc.
func TestCounterUnitSuffixStable(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	metricsOnce = sync.Once{}
	metricsInst = nil

	m := GetMetrics()
	m.RecordRateLimitRefusal("send")
	m.RecordAllowlistRefusal("send")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	wantUnits := map[string]string{
		"wa.rate_limit.refusals": "{refusal}",
		"wa.allowlist.refusals":  "{refusal}",
		"wa.event_queue.depth":   "{event}",
		"wa.goroutines":          "{goroutine}",
		"wa.fds":                 "{fd}",
		"wa.subscribe.streams":   "{stream}",
	}
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			want, ok := wantUnits[mm.Name]
			if !ok {
				continue
			}
			if mm.Unit != want {
				t.Errorf("%s unit = %q, want %q", mm.Name, mm.Unit, want)
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
