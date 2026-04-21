package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
)

// EventBufferFactory returns a fresh EventBuffer for one sub-test.
type EventBufferFactory func(t *testing.T) app.EventBuffer

// RunEventBufferContract runs every contract clause for EventBuffer per
// FR-060..FR-064.
//
//	EB1 Append assigns monotonic seq; Range returns insertion order.
//	EB2 Range with sinceSeq skips older entries.
//	EB3 TrimTo removes entries with seq <= arg.
//	EB4 Stats reports size/capacity/oldest/newest.
func RunEventBufferContract(t *testing.T, factory EventBufferFactory) {
	t.Helper()
	ctx := context.Background()

	mk := func(kind string, seq int64) app.EventRecord {
		return app.EventRecord{
			Seq:           seq,
			Kind:          kind,
			TrustedJSON:   []byte(`{}`),
			UntrustedJSON: []byte(`{}`),
			CreatedAt:     time.Unix(1_700_000_000+seq, 0),
		}
	}

	t.Run("EB1 monotonic seq + ordered Range", func(t *testing.T) {
		b := factory(t)
		for i := int64(1); i <= 3; i++ {
			if err := b.Append(ctx, mk("message", i)); err != nil {
				t.Fatalf("Append %d: %v", i, err)
			}
		}
		got, err := b.Range(ctx, 0, 10)
		if err != nil {
			t.Fatalf("Range: %v", err)
		}
		if len(got) != 3 || got[0].Seq > got[2].Seq {
			reportf(t, "EventBuffer", "Range", "EB1", "3 events in seq order", "unexpected ordering")
		}
	})

	t.Run("EB2 Range sinceSeq skips", func(t *testing.T) {
		b := factory(t)
		for i := int64(1); i <= 5; i++ {
			_ = b.Append(ctx, mk("message", i))
		}
		got, err := b.Range(ctx, 2, 10)
		if err != nil {
			t.Fatalf("Range sinceSeq=2: %v", err)
		}
		for _, r := range got {
			if r.Seq <= 2 {
				reportf(t, "EventBuffer", "Range", "EB2", "seq > sinceSeq", "included seq "+itoa64(r.Seq))
			}
		}
	})

	t.Run("EB3 TrimTo", func(t *testing.T) {
		b := factory(t)
		for i := int64(1); i <= 5; i++ {
			_ = b.Append(ctx, mk("message", i))
		}
		n, err := b.TrimTo(ctx, 3)
		if err != nil {
			t.Fatalf("TrimTo: %v", err)
		}
		if n != 3 {
			reportf(t, "EventBuffer", "TrimTo", "EB3", "3 trimmed", itoa(n)+" trimmed")
		}
	})

	t.Run("EB4 Stats", func(t *testing.T) {
		b := factory(t)
		for i := int64(1); i <= 3; i++ {
			_ = b.Append(ctx, mk("message", i))
		}
		st, err := b.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if st.Size != 3 {
			reportf(t, "EventBuffer", "Stats", "EB4", "Size=3", "Size="+itoa(st.Size))
		}
	})
}

func itoa64(n int64) string { return itoa(int(n)) }
