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
//	IS1 Check on unknown key ⇒ replayed=false, nil result.
//	IS2 Record then Check with matching fingerprint ⇒ replayed=true,
//	    same bytes.
//	IS3 Record then Check with mismatched fingerprint ⇒
//	    ErrIdempotencyConflict, no mutation.
//	IS4 Sweep removes rows whose expires_at <= before.
//	IS5 Profile isolation: same key in different profiles is independent.
//	IS6 ctx cancellation honoured on every method.
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

	// IS7..IS10 cover the FR-034a LoadOrStore contract. paramsHash lives
	// in the v4 (method, profile, key) sidecar; Check/Record above drive
	// the 017 table. Both surfaces share the Sweep sweeper.
	var ph1, ph2 [32]byte
	copy(ph1[:], []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	copy(ph2[:], []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))

	t.Run("IS7 LoadOrStore empty key bypasses", func(t *testing.T) {
		s := factory(t)
		fires := 0
		for i := 0; i < 3; i++ {
			if _, err := s.LoadOrStore(ctx, "send", "default", "", ph1, func() ([]byte, error) {
				fires++
				return []byte(`ok`), nil
			}); err != nil {
				t.Fatalf("LoadOrStore empty: %v", err)
			}
		}
		if fires != 3 {
			reportf(t, "IdempotencyStore", "LoadOrStore", "IS7", "fires=3", "fires="+itoa(fires))
		}
	})

	t.Run("IS8 LoadOrStore replay byte-identical", func(t *testing.T) {
		s := factory(t)
		payload := []byte(`{"messageId":"M1","timestamp":123}`)
		got1, err := s.LoadOrStore(ctx, "send", "default", "k1", ph1, func() ([]byte, error) {
			return payload, nil
		})
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		if string(got1) != string(payload) {
			reportf(t, "IdempotencyStore", "LoadOrStore", "IS8", string(payload), string(got1))
		}
		executed := false
		got2, err := s.LoadOrStore(ctx, "send", "default", "k1", ph1, func() ([]byte, error) {
			executed = true
			return []byte(`{"ignored":true}`), nil
		})
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if executed {
			reportf(t, "IdempotencyStore", "LoadOrStore", "IS8", "execute skipped", "execute fired")
		}
		if string(got2) != string(payload) {
			reportf(t, "IdempotencyStore", "LoadOrStore", "IS8", string(payload), string(got2))
		}
	})

	t.Run("IS9 LoadOrStore collision returns domain err", func(t *testing.T) {
		s := factory(t)
		if _, err := s.LoadOrStore(ctx, "send", "default", "k1", ph1, func() ([]byte, error) {
			return []byte(`ok`), nil
		}); err != nil {
			t.Fatalf("first: %v", err)
		}
		_, err := s.LoadOrStore(ctx, "send", "default", "k1", ph2, func() ([]byte, error) {
			t.Fatal("execute must not fire on collision")
			return nil, nil
		})
		if !errors.Is(err, domain.ErrIdempotencyCollision) {
			got := "<nil>"
			if err != nil {
				got = err.Error()
			}
			reportf(t, "IdempotencyStore", "LoadOrStore", "IS9", "ErrIdempotencyCollision", got)
		}
	})

	t.Run("IS10 Sweep evicts v4 rows", func(t *testing.T) {
		s := factory(t)
		if _, err := s.LoadOrStore(ctx, "send", "default", "k1", ph1, func() ([]byte, error) {
			return []byte(`ok`), nil
		}); err != nil {
			t.Fatalf("store: %v", err)
		}
		// 24h TTL default → sweep far future evicts.
		if _, err := s.Sweep(ctx, time.Now().Add(48*time.Hour)); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		fires := 0
		if _, err := s.LoadOrStore(ctx, "send", "default", "k1", ph1, func() ([]byte, error) {
			fires++
			return []byte(`ok`), nil
		}); err != nil {
			t.Fatalf("post-sweep: %v", err)
		}
		if fires != 1 {
			reportf(t, "IdempotencyStore", "LoadOrStore", "IS10", "fires=1 after sweep", "fires="+itoa(fires))
		}
	})
}

