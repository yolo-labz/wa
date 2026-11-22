package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// meterReader routes the daemon's instruments to a manual reader and returns
// a func that reads every counter data point as "name{k=v,...}" -> value.
func meterReader(t *testing.T) func() map[string]int64 {
	t.Helper()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	observability.ResetMetricsForTest()
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()); observability.ResetMetricsForTest() })
	return func() map[string]int64 {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, sm := range rm.ScopeMetrics {
			for _, mm := range sm.Metrics {
				if sum, ok := mm.Data.(metricdata.Sum[int64]); ok {
					for _, dp := range sum.DataPoints {
						var kv []string
						for it := dp.Attributes.Iter(); it.Next(); {
							kv = append(kv, fmt.Sprintf("%s=%s", it.Attribute().Key, it.Attribute().Value.AsString()))
						}
						out[mm.Name+"{"+strings.Join(kv, ",")+"}"] += dp.Value
					}
				}
			}
		}
		return out
	}
}

// Every wire send is counted by outcome, and a FRESH send_uncertain also by
// cause class and as a Warn log line; neither carries the message id.
func TestSendOnWire_CountsOutcomesAndCauses(t *testing.T) {
	cases := []struct {
		name  string
		setup func(fc *fakeWhatsmeowClient)
		want  []string // series that must read 1
	}{
		{"ok", func(*fakeWhatsmeowClient) {}, []string{"wa.send.outcome{wa.method=send,wa.outcome=ok}"}},
		{"plain error", func(fc *fakeWhatsmeowClient) { fc.SendErr = errors.New("boom") }, []string{"wa.send.outcome{wa.method=send,wa.outcome=plain_error}"}},
		{"ack timeout", func(fc *fakeWhatsmeowClient) { fc.SendErr = waClient.ErrMessageTimedOut }, []string{"wa.send.outcome{wa.method=send,wa.outcome=uncertain}", "wa.send.uncertain.cause{wa.cause=ack_timeout}"}},
		{"disconnected", func(fc *fakeWhatsmeowClient) { fc.SendErr = &waClient.DisconnectedError{Action: "message send"} }, []string{"wa.send.uncertain.cause{wa.cause=disconnected}"}},
		{"not connected", func(fc *fakeWhatsmeowClient) { fc.SendErr, fc.SendWrote = waClient.ErrNotConnected, true }, []string{"wa.send.uncertain.cause{wa.cause=not_connected}"}},
		{"iq timeout", func(fc *fakeWhatsmeowClient) { fc.SendErr, fc.SendWrote = waClient.ErrIQTimedOut, true }, []string{"wa.send.uncertain.cause{wa.cause=iq_timeout}"}},
		{"ctx end", func(fc *fakeWhatsmeowClient) { fc.SendErr, fc.SendWrote = context.Canceled, true }, []string{"wa.send.uncertain.cause{wa.cause=ctx_end}"}},
		{"resend error", func(fc *fakeWhatsmeowClient) { fc.SendErr, fc.SendWrote = errors.New("write tcp: broken pipe"), true }, []string{"wa.send.uncertain.cause{wa.cause=resend_error}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			read := meterReader(t)
			var logs strings.Builder
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })
			fc := newFakeClient()
			fc.ConnectedFlag = true
			fc.SendErrID = "WAID-SECRET"
			c.setup(fc)
			a := newTestAdapter(t, fc)
			t.Cleanup(func() { _ = a.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = a.Send(ctx, domain.TextMessage{Recipient: mustParseJIDT(wireTestChat), Body: "hi"})
			got := read()
			for _, w := range c.want {
				if got[w] != 1 {
					t.Errorf("%s = %d, want 1 (all: %v)", w, got[w], got)
				}
			}
			uncertain := strings.Contains(c.want[len(c.want)-1], "uncertain")
			if warned := strings.Contains(logs.String(), "send_uncertain"); warned != uncertain {
				t.Errorf("warn logged=%v, want %v: %q", warned, uncertain, logs.String())
			}
			if uncertain && !strings.Contains(logs.String(), "method=send") {
				t.Errorf("warn lacks the method: %q", logs.String())
			}
			for k := range got {
				if strings.Contains(k, "WAID") || strings.Contains(k, wireTestChat) {
					t.Errorf("series %q carries an id or jid", k)
				}
			}
			if strings.Contains(logs.String(), "WAID-SECRET") || strings.Contains(logs.String(), wireTestChat) {
				t.Errorf("log carries the message id or jid: %q", logs.String())
			}
		})
	}
}
