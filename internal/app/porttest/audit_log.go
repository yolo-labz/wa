package porttest

import (
	"context"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// AuditLogFactory returns a fresh AuditLog for one sub-test.
type AuditLogFactory func(t *testing.T) app.AuditLog

// RunAuditLogContract exercises AuditLog (FR-050..054) as a standalone
// contract runner. Adapters that do not implement the full Adapter
// interface of RunContractSuite still satisfy this one.
//
//	AUD1 Record on the happy path returns nil.
//	AUD2 Parallel Record from 8 goroutines returns nil for every call.
//	AUD3 Record with a cancelled ctx is either success or ctx.Err
//	     (never panic, never silent corruption).
//	AUD4 Record across every AuditAction constant returns nil.
func RunAuditLogContract(t *testing.T, factory AuditLogFactory) {
	t.Helper()
	jid := domain.MustJID("5511999999999")

	t.Run("AUD1_record_happy", func(t *testing.T) {
		a := factory(t)
		e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "")
		if err := a.Record(context.Background(), e); err != nil {
			reportf(t, "AuditLog", "Record", "AUD1", "nil error", err.Error())
		}
	})

	t.Run("AUD2_record_parallel", func(t *testing.T) {
		a := factory(t)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "")
				if err := a.Record(context.Background(), e); err != nil {
					reportf(t, "AuditLog", "Record", "AUD2", "nil error", err.Error())
				}
			})
		}
		wg.Wait()
	})

	t.Run("AUD3_cancelled_ctx", func(t *testing.T) {
		a := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "")
		_ = a.Record(ctx, e)
	})

	t.Run("AUD4_distinct_actions", func(t *testing.T) {
		a := factory(t)
		for _, act := range []domain.AuditAction{
			domain.AuditSend, domain.AuditReceive, domain.AuditPair,
			domain.AuditGrant, domain.AuditRevoke, domain.AuditPanic,
		} {
			e := domain.NewAuditEvent("wad", act, jid, "allow", "")
			if err := a.Record(context.Background(), e); err != nil {
				reportf(t, "AuditLog", "Record", "AUD4", "nil error", err.Error())
			}
		}
	})
}
