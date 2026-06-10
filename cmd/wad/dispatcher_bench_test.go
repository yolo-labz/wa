package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// BenchmarkDispatcherStatus measures the cheapest full method
// round-trip through the dispatcher (parse → handle → marshal).
// Backing bench/README.md (roadmap 2.2).
func BenchmarkDispatcherStatus(b *testing.B) {
	mem := memory.New(nil)
	log := slog.New(slog.DiscardHandler)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: mem, Events: mem, Contacts: mem, Groups: mem, Session: mem,
		Allowlist: mem, Audit: mem, History: mem, Pairer: mem,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		Logger:         log,
	})
	b.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := d.Handle(ctx, "status", nil); err != nil {
			b.Fatal(err)
		}
	}
}
