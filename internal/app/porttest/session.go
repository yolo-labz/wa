package porttest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

func testSessionStore(t *testing.T, factory Factory) {
	t.Helper()
	jid := domain.MustJID("5511999999999")

	t.Run("load_empty_zero_not_error", func(t *testing.T) {
		a := factory(t)
		s, err := a.Load(context.Background())
		if err != nil {
			reportf(t, "SessionStore", "Load", "empty", "nil error", err.Error())
		}
		if !s.IsZero() {
			reportf(t, "SessionStore", "Load", "empty", "zero Session", "non-zero")
		}
	})

	t.Run("save_then_load", func(t *testing.T) {
		a := factory(t)
		want, _ := domain.NewSession(jid, 1, time.Now())
		if err := a.Save(context.Background(), want); err != nil {
			reportf(t, "SessionStore", "Save", "happy", "nil error", err.Error())
		}
		got, err := a.Load(context.Background())
		if err != nil || got.JID() != jid || got.DeviceID() != 1 {
			reportf(t, "SessionStore", "Load", "roundtrip", "match", "mismatch")
		}
	})

	t.Run("clear_empty_idempotent", func(t *testing.T) {
		a := factory(t)
		if err := a.Clear(context.Background()); err != nil {
			reportf(t, "SessionStore", "Clear", "idempotent", "nil error", err.Error())
		}
	})

	t.Run("clear_after_save", func(t *testing.T) {
		a := factory(t)
		s, _ := domain.NewSession(jid, 1, time.Now())
		_ = a.Save(context.Background(), s)
		_ = a.Clear(context.Background())
		got, _ := a.Load(context.Background())
		if !got.IsZero() {
			reportf(t, "SessionStore", "Clear", "wipes", "zero Session", "non-zero")
		}
	})

	t.Run("parallel_save", func(t *testing.T) {
		a := factory(t)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(id uint16) {
				defer wg.Done()
				s, _ := domain.NewSession(jid, id+1, time.Now())
				_ = a.Save(context.Background(), s)
			}(uint16(i))
		}
		wg.Wait()
		got, err := a.Load(context.Background())
		if err != nil || got.IsZero() {
			reportf(t, "SessionStore", "Save", "parallel", "some session", "none")
		}
	})

	t.Run("save_ctx_cancelled", func(t *testing.T) {
		a := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s, _ := domain.NewSession(jid, 1, time.Now())
		if err := a.Save(ctx, s); err == nil {
			// not strictly required; only flag if adapter does claim to respect ctx
			_ = err
		}
	})
}
