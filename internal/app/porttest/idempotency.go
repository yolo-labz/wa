package porttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// IdempotencyStoreFactory returns a fresh IdempotencyStore for one sub-test.
type IdempotencyStoreFactory func(t *testing.T) app.IdempotencyStore

// RunIdempotencyStoreContract runs every contract clause for the
// IdempotencyStore port per FR-030..FR-033. Clauses:
//
//   IS1 Check on unknown key ⇒ replayed=false, nil result.
//   IS2 Record then Check with matching fingerprint ⇒ replayed=true,
//       same bytes.
//   IS3 Record then Check with mismatched fingerprint ⇒
//       ErrIdempotencyConflict, no mutation.
//   IS4 Sweep removes rows whose expires_at <= before.
//   IS5 Profile isolation: same key in different profiles is independent.
//   IS6 ctx cancellation honoured on every method.
func RunIdempotencyStoreContract(t *testing.T, factory IdempotencyStoreFactory) {
	t.Helper()
	ctx := context.Background()

	fp1 := domain.ComputeFingerprint([]byte(`{"a":1}`))
	fp2 := domain.ComputeFingerprint([]byte(`{"a":2}`))
	k1, err := domain.NewIdempotencyKey("ik_a", fp1)
	if err != nil {
		t.Fatalf("NewIdempotencyKey: %v", err)
	}
	k1prime, _ := domain.NewIdempotencyKey("ik_a", fp2)

	t.Run("IS1 Check on unknown key", func(t *testing.T) {
		s := factory(t)
		result, replayed, err := s.Check(ctx, "default", k1)
		if err != nil {
			reportf(t, "IdempotencyStore", "Check", "IS1", "nil error", err.Error())
		}
		if replayed {
			reportf(t, "IdempotencyStore", "Check", "IS1", "replayed=false", "replayed=true")
		}
		if result != nil {
			reportf(t, "IdempotencyStore", "Check", "IS1", "nil result", "non-nil result")
		}
	})

	t.Run("IS2 Record then replay", func(t *testing.T) {
		s := factory(t)
		payload := []byte(`{"messageId":"wa_abc","ts":1}`)
		if err := s.Record(ctx, "default", k1, payload, time.Now().Add(24*time.Hour)); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, replayed, err := s.Check(ctx, "default", k1)
		if err != nil {
			reportf(t, "IdempotencyStore", "Check", "IS2", "nil error", err.Error())
		}
		if !replayed {
			reportf(t, "IdempotencyStore", "Check", "IS2", "replayed=true", "replayed=false")
		}
		if string(got) != string(payload) {
			reportf(t, "IdempotencyStore", "Check", "IS2", string(payload), string(got))
		}
	})

	t.Run("IS3 fingerprint mismatch", func(t *testing.T) {
		s := factory(t)
		payload := []byte(`{"messageId":"wa_abc","ts":1}`)
		if err := s.Record(ctx, "default", k1, payload, time.Now().Add(24*time.Hour)); err != nil {
			t.Fatalf("Record: %v", err)
		}
		_, _, err := s.Check(ctx, "default", k1prime)
		if !errors.Is(err, domain.ErrIdempotencyConflict) {
			reportf(t, "IdempotencyStore", "Check", "IS3", "ErrIdempotencyConflict", err.Error())
		}
	})

	t.Run("IS4 Sweep expired", func(t *testing.T) {
		s := factory(t)
		expired := time.Now().Add(-1 * time.Hour)
		if err := s.Record(ctx, "default", k1, []byte(`{}`), expired); err != nil {
			t.Fatalf("Record expired: %v", err)
		}
		n, err := s.Sweep(ctx, time.Now())
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if n != 1 {
			reportf(t, "IdempotencyStore", "Sweep", "IS4", "deleted=1", "deleted="+itoa(n))
		}
		_, replayed, _ := s.Check(ctx, "default", k1)
		if replayed {
			reportf(t, "IdempotencyStore", "Check", "IS4", "replayed=false after sweep", "replayed=true")
		}
	})

	t.Run("IS5 profile isolation", func(t *testing.T) {
		s := factory(t)
		if err := s.Record(ctx, "work", k1, []byte(`{"p":"work"}`), time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("Record work: %v", err)
		}
		_, replayed, err := s.Check(ctx, "personal", k1)
		if err != nil {
			reportf(t, "IdempotencyStore", "Check", "IS5", "nil error (other profile)", err.Error())
		}
		if replayed {
			reportf(t, "IdempotencyStore", "Check", "IS5", "replayed=false across profiles", "replayed=true")
		}
	})

	t.Run("IS6 ctx cancelled", func(t *testing.T) {
		s := factory(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, _, err := s.Check(cctx, "default", k1)
		if !errors.Is(err, context.Canceled) {
			reportf(t, "IdempotencyStore", "Check", "IS6", "context.Canceled", err.Error())
		}
	})
}

