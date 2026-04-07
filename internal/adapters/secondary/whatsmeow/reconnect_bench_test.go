package whatsmeow

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// BenchmarkReconnectLatency measures the wall time between enqueuing a
// Disconnected event and observing a follow-up Connected event flow through
// the bounded EventStream against the in-process fake client. It exists as a
// deterministic guard for spec SC-007 ("reconnect after restart <5s"); the
// real-world verification is the burner-only manual quickstart path.
func BenchmarkReconnectLatency(b *testing.B) {
	fc := newFakeClient()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := openWithClient(fc, domain.NewAllowlist(), logger, fixedNowFn)
	b.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.EnqueueEvent(domain.ConnectionEvent{ID: "d", TS: fixedNow, State: domain.ConnDisconnected})
		a.EnqueueEvent(domain.ConnectionEvent{ID: "c", TS: fixedNow, State: domain.ConnConnected})
		if _, err := a.Next(ctx); err != nil {
			b.Fatalf("Next disc: %v", err)
		}
		if _, err := a.Next(ctx); err != nil {
			b.Fatalf("Next conn: %v", err)
		}
	}
}
