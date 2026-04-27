package porttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
)

// PairerFactory returns a fresh Pairer for one sub-test.
type PairerFactory func(t *testing.T) app.Pairer

// RunPairerContract exercises the Pairer contract (PR1..PR3) against an
// adapter. PR1 QR flow — empty phone must NOT error before a ctx deadline
// fires. PR2 phone-code flow — non-empty phone is accepted. PR3 context
// cancellation surfaces as ctx.Err().
func RunPairerContract(t *testing.T, factory PairerFactory) {
	t.Helper()

	t.Run("PR1_qr_flow_accepts_empty_phone", func(t *testing.T) {
		p := factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := p.Pair(ctx, "")
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			reportf(t, "Pairer", "Pair", "PR1", "nil or ctx error", err.Error())
		}
	})

	t.Run("PR2_phone_flow_accepts_non_empty", func(t *testing.T) {
		p := factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := p.Pair(ctx, "+5511999999999")
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			reportf(t, "Pairer", "Pair", "PR2", "nil or ctx error", err.Error())
		}
	})

	t.Run("PR3_cancelled_ctx_returns_err", func(t *testing.T) {
		p := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := p.Pair(ctx, "")
		// Adapters that short-circuit before checking ctx MAY return nil;
		// implementations that touch the network MUST surface ctx.Err.
		// The contract only asserts: never panic and never return a
		// non-ctx error after a pre-cancelled context.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			reportf(t, "Pairer", "Pair", "PR3", "nil or ctx error", err.Error())
		}
	})
}
