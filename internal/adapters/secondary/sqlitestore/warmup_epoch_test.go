package sqlitestore

import (
	"context"
	"testing"
	"time"
)

// TestWarmupEpochRoundTrip — the column has to survive a reopen, because
// surviving restarts is the entire reason it exists (issue #368).
func TestWarmupEpochRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/session.db"

	store, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, known, err := store.WarmupEpoch(ctx); err != nil || known {
		t.Fatalf("fresh store: got known=%v err=%v, want unknown", known, err)
	}

	want := time.Unix(1780000000, 0).UTC()
	if err := store.SetWarmupEpoch(ctx, want); err != nil {
		t.Fatalf("SetWarmupEpoch: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, known, err := reopened.WarmupEpoch(ctx)
	if err != nil || !known {
		t.Fatalf("after reopen: known=%v err=%v, want known", known, err)
	}
	if !got.Equal(want) {
		t.Errorf("epoch = %s, want %s", got, want)
	}
}

// TestWarmupEpochIndependentOfPairedAt — the two columns answer different
// questions and must not alias. Writing one may never move the other, or
// health's honestly-absent sessionSince (issue #311) starts reporting the
// warmup adoption instant as if it were the pairing handshake.
func TestWarmupEpochIndependentOfPairedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/session.db", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	warmup := time.Unix(1780000000, 0).UTC()
	if err := store.SetWarmupEpoch(ctx, warmup); err != nil {
		t.Fatalf("SetWarmupEpoch: %v", err)
	}

	// paired_at must still be unknown: warmup adoption is not a handshake.
	if _, known, err := store.PairedAt(ctx); err != nil || known {
		t.Fatalf("PairedAt leaked from warmup write: known=%v err=%v", known, err)
	}

	paired := time.Unix(1700000000, 0).UTC()
	if err := store.SetPairedAt(ctx, paired); err != nil {
		t.Fatalf("SetPairedAt: %v", err)
	}
	got, known, err := store.WarmupEpoch(ctx)
	if err != nil || !known {
		t.Fatalf("WarmupEpoch after SetPairedAt: known=%v err=%v", known, err)
	}
	if !got.Equal(warmup) {
		t.Errorf("SetPairedAt clobbered warmup_epoch: got %s, want %s", got, warmup)
	}
}

// TestWarmupEpochZeroClears — a re-pair starts a new ladder.
func TestWarmupEpochZeroClears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/session.db", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetWarmupEpoch(ctx, time.Unix(1780000000, 0)); err != nil {
		t.Fatalf("SetWarmupEpoch: %v", err)
	}
	if err := store.SetWarmupEpoch(ctx, time.Time{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, known, err := store.WarmupEpoch(ctx); err != nil || known {
		t.Errorf("after clear: known=%v err=%v, want unknown", known, err)
	}
}

// TestEnsureSessionMetaSchemaAddsWarmupColumn — an existing database from a
// build that predates the column must gain it rather than error. This is
// the upgrade path every deployed daemon takes.
func TestEnsureSessionMetaSchemaAddsWarmupColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/session.db", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// session_meta is created lazily by EnsureSessionMetaSchema, not by
	// Open, so materialise it before simulating the pre-#368 shape.
	if err := EnsureSessionMetaSchema(ctx, store.db); err != nil {
		t.Fatalf("EnsureSessionMetaSchema: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE session_meta DROP COLUMN warmup_epoch`); err != nil {
		t.Fatalf("simulate the old schema: %v", err)
	}
	has, err := hasColumn(ctx, store.db, "session_meta", "warmup_epoch")
	if err != nil || has {
		t.Fatalf("precondition: column still present (has=%v err=%v)", has, err)
	}

	if err := EnsureSessionMetaSchema(ctx, store.db); err != nil {
		t.Fatalf("EnsureSessionMetaSchema on the old shape: %v", err)
	}
	has, err = hasColumn(ctx, store.db, "session_meta", "warmup_epoch")
	if err != nil || !has {
		t.Fatalf("column not re-added (has=%v err=%v)", has, err)
	}
	// And it must be usable, not merely present.
	if _, _, err := store.WarmupEpoch(ctx); err != nil {
		t.Errorf("WarmupEpoch after upgrade: %v", err)
	}
}
