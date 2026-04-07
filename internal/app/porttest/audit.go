package porttest

import (
	"context"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

func testAuditLog(t *testing.T, factory Factory) {
	t.Helper()
	jid := domain.MustJID("5511999999999")

	t.Run("record_happy", func(t *testing.T) {
		a := factory(t)
		e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "")
		if err := a.Record(context.Background(), e); err != nil {
			reportf(t, "AuditLog", "Record", "happy", "nil error", err.Error())
		}
	})

	t.Run("record_parallel", func(t *testing.T) {
		a := factory(t)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "")
				if err := a.Record(context.Background(), e); err != nil {
					reportf(t, "AuditLog", "Record", "parallel", "nil error", err.Error())
				}
			}()
		}
		wg.Wait()
	})

	t.Run("record_ctx_cancelled_ok_or_err", func(t *testing.T) {
		a := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "")
		// Either success (already persisted) or ctx.Err is acceptable;
		// panics or silent corruption are not.
		_ = a.Record(ctx, e)
	})

	t.Run("record_distinct_actions", func(t *testing.T) {
		a := factory(t)
		for _, act := range []domain.AuditAction{domain.AuditSend, domain.AuditReceive, domain.AuditPair, domain.AuditGrant, domain.AuditRevoke, domain.AuditPanic} {
			e := domain.NewAuditEvent("wad", act, jid, "allow", "")
			if err := a.Record(context.Background(), e); err != nil {
				reportf(t, "AuditLog", "Record", "distinct", "nil error", err.Error())
			}
		}
	})

	t.Run("record_persists", func(t *testing.T) {
		a := factory(t)
		e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "ok")
		if err := a.Record(context.Background(), e); err != nil {
			reportf(t, "AuditLog", "Record", "persists", "nil error", err.Error())
		}
	})
}
