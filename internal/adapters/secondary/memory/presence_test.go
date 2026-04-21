package memory_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/domain"
)

// TestComposingRateLimit1Hz asserts FR-071: one accepted SendComposing per
// chat per second; additional calls in the window drop silently (nil err).
// Uses testing/synctest to pin the clock without real sleeps.
func TestComposingRateLimit1Hz(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Unix(1_700_000_000, 0).UTC()
		clk := &memory.FakeClock{T: start}
		a := memory.New(clk)
		chat := domain.MustJID("5511999999999")
		ctx := context.Background()

		if err := a.SendComposing(ctx, chat, "composing", time.Second); err != nil {
			t.Fatalf("first composing: %v", err)
		}

		clk.Advance(500 * time.Millisecond)
		if err := a.SendComposing(ctx, chat, "composing", time.Second); err != nil {
			t.Fatalf("second composing within 1s: %v", err)
		}

		clk.Advance(600 * time.Millisecond)
		if err := a.SendComposing(ctx, chat, "composing", time.Second); err != nil {
			t.Fatalf("third composing after 1.1s: %v", err)
		}

		emissions := a.ComposingEmissions()
		if len(emissions) != 1 {
			t.Fatalf("expected 1 chat emission, got %d", len(emissions))
		}
		if emissions[0].Accepted != 2 {
			t.Errorf("accepted = %d, want 2", emissions[0].Accepted)
		}
		if emissions[0].Dropped != 1 {
			t.Errorf("dropped = %d, want 1", emissions[0].Dropped)
		}
	})
}

func TestComposingEmptyStateRejected(t *testing.T) {
	t.Parallel()
	a := memory.New(nil)
	chat := domain.MustJID("5511999999999")
	if err := a.SendComposing(context.Background(), chat, "", time.Second); err == nil {
		t.Fatal("empty state must return error")
	}
}
