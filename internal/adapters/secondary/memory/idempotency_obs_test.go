package memory

import (
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/yolo-labz/wa/v2/internal/observability"
)

// The in-memory store reports its flights on wa.idempotency.flight too.
func TestIdempotencyStore_CountsFlights(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	observability.ResetMetricsForTest()
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()); observability.ResetMetricsForTest() })

	var h [32]byte
	if _, err := NewIdempotencyStore().LoadOrStore(t.Context(), "send", "default", "k", h, func() ([]byte, error) { return []byte("{}"), nil }); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			if sum, ok := mm.Data.(metricdata.Sum[int64]); ok && mm.Name == "wa.idempotency.flight" {
				for _, dp := range sum.DataPoints {
					n += dp.Value
				}
			}
		}
	}
	if n != 1 {
		t.Fatalf("wa.idempotency.flight = %d, want 1 leader", n)
	}
}
