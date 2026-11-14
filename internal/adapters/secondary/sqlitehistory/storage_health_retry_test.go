package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
)

type codedErr int

func (c codedErr) Error() string { return fmt.Sprintf("sqlite code %d", int(c)) }
func (c codedErr) Code() int     { return int(c) }

// TestRetryBusy_DeadlineAlwaysClassifiesAsStorageFailure pins the F3 root
// cause: when the 5 s budget expires, the interrupted write surfaces as
// BUSY, SQLITE_INTERRUPT, sql.ErrTxDone or a context error depending on
// which side of the driver wins the race. Every one must latch storage.
func TestRetryBusy_DeadlineAlwaysClassifiesAsStorageFailure(t *testing.T) {
	surfaces := map[string]error{
		"busy":      codedErr(5),
		"interrupt": fmt.Errorf("sqlitehistory: begin: %w", codedErr(9)),
		"txdone":    fmt.Errorf("sqlitehistory: prepare: %w", sql.ErrTxDone),
		"deadline":  context.DeadlineExceeded,
		"plain":     errors.New("anything the driver says while it is interrupted"),
	}
	for name, surface := range surfaces {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				err := retryBusy(context.Background(), func(ctx context.Context) error {
					<-ctx.Done() // the write hangs until the budget expires
					return surface
				})
				if !storageFailure(err) {
					t.Fatalf("deadline surfaced as %v; want a storage failure", err)
				}
			})
		})
	}
}

func TestRetryBusy_NotOurDeadlineIsNotAStorageFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		err := retryBusy(parent, func(ctx context.Context) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		})
		if err == nil || storageFailure(err) {
			t.Fatalf("a cancelled caller must not latch storage: %v", err)
		}
		if err := retryBusy(context.Background(), func(context.Context) error { return errors.New("logic") }); err == nil || storageFailure(err) {
			t.Fatalf("a logic error before the deadline must stay a logic error: %v", err)
		}
	})
}
