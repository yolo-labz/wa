package porttest

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

// runHistoryStoreContract exercises the HS1–HS6 clauses from
// specs/003-whatsmeow-adapter/contracts/historystore.md.
//
// Adapters that do NOT support remote backfill (e.g. the in-memory
// adapter) return false from SupportsRemoteBackfill() and HS2 is
// skipped. HS3 remains the observable "local returns empty, no error"
// guarantee for those adapters.
//
//nolint:gocyclo // contract test fan-out across HS1-HS6; splitting hurts readability
func runHistoryStoreContract(t *testing.T, factory Factory) {
	t.Helper()

	chat := domain.MustJID("5511999999999")

	t.Run("HS1_local_happy", func(t *testing.T) {
		a := factory(t)
		// Seed 5 messages in insertion (ascending-timestamp) order.
		for i := 0; i < 5; i++ {
			a.AppendHistory(chat, domain.TextMessage{Recipient: chat, Body: body(i)})
		}
		msgs, err := a.LoadMore(context.Background(), chat, domain.MessageID(""), 10)
		if err != nil {
			reportf(t, "HistoryStore", "LoadMore", "HS1", "nil error", err.Error())
		}
		if len(msgs) != 5 {
			reportf(t, "HistoryStore", "LoadMore", "HS1", "5 messages", itoa(len(msgs)))
		}
		// Descending order: the last-appended message must come first.
		if len(msgs) == 5 {
			first, _ := msgs[0].(domain.TextMessage)
			if first.Body != body(4) {
				reportf(t, "HistoryStore", "LoadMore", "HS1", "newest-first (body="+body(4)+")", "body="+first.Body)
			}
		}
	})

	t.Run("HS2_remote_backfill", func(t *testing.T) {
		a := factory(t)
		if !a.SupportsRemoteBackfill() {
			t.Skip("adapter does not support remote backfill; HS2 is whatsmeow-only")
		}
		// Adapters that DO support remote backfill provide their own
		// coverage inside their package (the suite cannot drive a real
		// remote round-trip without an adapter-specific fake). The
		// skeletal assertion here is that the call does not panic and
		// returns a well-formed empty-or-populated slice with no
		// ErrDisconnected classification.
		msgs, err := a.LoadMore(context.Background(), chat, domain.MessageID(""), 10)
		if err != nil && errors.Is(err, domain.ErrDisconnected) {
			reportf(t, "HistoryStore", "LoadMore", "HS2", "non-ErrDisconnected", err.Error())
		}
		_ = msgs
	})

	t.Run("HS3_local_empty_no_error", func(t *testing.T) {
		a := factory(t)
		if a.SupportsRemoteBackfill() {
			t.Skip("adapter supports remote backfill; HS3 is local-only")
		}
		msgs, err := a.LoadMore(context.Background(), chat, domain.MessageID(""), 10)
		if err != nil {
			reportf(t, "HistoryStore", "LoadMore", "HS3", "nil error", err.Error())
		}
		if len(msgs) != 0 {
			reportf(t, "HistoryStore", "LoadMore", "HS3", "empty slice", itoa(len(msgs)))
		}
	})

	t.Run("HS4_zero_jid", func(t *testing.T) {
		a := factory(t)
		msgs, err := a.LoadMore(context.Background(), domain.JID{}, domain.MessageID(""), 10)
		if !errors.Is(err, domain.ErrInvalidJID) {
			reportf(t, "HistoryStore", "LoadMore", "HS4", "err wrapping ErrInvalidJID", errString(err))
		}
		if msgs != nil {
			reportf(t, "HistoryStore", "LoadMore", "HS4", "nil slice", "non-nil")
		}
	})

	t.Run("HS5_invalid_limit", func(t *testing.T) {
		a := factory(t)
		for _, lim := range []int{0, -1} {
			msgs, err := a.LoadMore(context.Background(), chat, domain.MessageID(""), lim)
			if err == nil {
				reportf(t, "HistoryStore", "LoadMore", "HS5", "non-nil error", "nil")
			}
			if msgs != nil {
				reportf(t, "HistoryStore", "LoadMore", "HS5", "nil slice", "non-nil")
			}
		}
	})

	t.Run("HS6_ctx_cancelled", func(t *testing.T) {
		a := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		msgs, err := a.LoadMore(ctx, chat, domain.MessageID(""), 10)
		if !errors.Is(err, context.Canceled) {
			reportf(t, "HistoryStore", "LoadMore", "HS6", "context.Canceled", errString(err))
		}
		if msgs != nil {
			reportf(t, "HistoryStore", "LoadMore", "HS6", "nil slice", "non-nil")
		}
	})
}

func body(i int) string {
	return "msg-" + itoa(i)
}

func itoa(i int) string {
	// small helper to avoid pulling strconv into this file when the
	// numbers are tiny and bounded by the contract test shape.
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
