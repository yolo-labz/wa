package sqliteevents_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
)

func openBuffer(t *testing.T, capacity int) *sqliteevents.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := sqliteevents.Open(context.Background(), filepath.Join(dir, "events.db"), capacity)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestEventsMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.db")
	for i := range 3 {
		s, err := sqliteevents.Open(context.Background(), path, 0)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

func TestEventBufferContractSqlite(t *testing.T) {
	factory := func(t *testing.T) app.EventBuffer {
		return openBuffer(t, 100)
	}
	porttest.RunEventBufferContract(t, factory)
}

func TestRingBufferDropOldest(t *testing.T) {
	s := openBuffer(t, 5)
	ctx := context.Background()
	for i := range 8 {
		if err := s.Append(ctx, app.EventRecord{
			Kind:        "message",
			TrustedJSON: []byte(`{}`),
			CreatedAt:   time.Unix(1_700_000_000+int64(i), 0),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Size != 5 {
		t.Fatalf("Size: got %d want 5", st.Size)
	}
	if st.DroppedTotal != 3 {
		t.Fatalf("DroppedTotal: got %d want 3", st.DroppedTotal)
	}
	if st.OldestSeq != 4 || st.NewestSeq != 8 {
		t.Fatalf("seq window: oldest=%d newest=%d want 4..8", st.OldestSeq, st.NewestSeq)
	}
}

func TestSyntheticStreamDrop(t *testing.T) {
	// No drop when oldest is contiguous with sinceSeq+1.
	if _, ok := sqliteevents.SyntheticDrop(10, 11, 0); ok {
		t.Fatalf("contiguous resume: want ok=false")
	}
	// Gap: sinceSeq=10, oldest=14 means seq 11..13 were evicted.
	rec, ok := sqliteevents.SyntheticDrop(10, 14, 7)
	if !ok {
		t.Fatalf("gapped resume: want ok=true")
	}
	if rec.Kind != "stream.drop" {
		t.Fatalf("Kind: got %q want stream.drop", rec.Kind)
	}
	if len(rec.TrustedJSON) == 0 {
		t.Fatalf("TrustedJSON is empty")
	}
}
