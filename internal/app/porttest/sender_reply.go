package porttest

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// RunReplySenderContract exercises the FR-070 ReplySender contract (RS1..RS4).
// It is invoked by adapter _test.go files that wire their own factory. Any
// adapter satisfying app.ReplySender MUST pass the full suite.
func RunReplySenderContract(t *testing.T, factory func(t *testing.T) app.ReplySender) {
	t.Helper()
	to := domain.MustJID("5511999999999")
	okMsg := domain.TextMessage{Recipient: to, Body: "yo"}

	t.Run("RS1_happy", func(t *testing.T) {
		rs := factory(t)
		id, err := rs.SendReply(context.Background(), "orig-1", okMsg)
		if err != nil {
			reportf(t, "ReplySender", "SendReply", "RS1", "nil err", err.Error())
		}
		if id == "" {
			reportf(t, "ReplySender", "SendReply", "RS1", "non-empty id", "empty")
		}
	})

	t.Run("RS2_zero_quotedID_rejected", func(t *testing.T) {
		rs := factory(t)
		_, err := rs.SendReply(context.Background(), "", okMsg)
		if err == nil {
			reportf(t, "ReplySender", "SendReply", "RS2", "non-nil err for zero quotedID", "nil")
		}
	})

	t.Run("RS3_validation_fails_before_io", func(t *testing.T) {
		rs := factory(t)
		empty := domain.TextMessage{Recipient: to, Body: ""}
		_, err := rs.SendReply(context.Background(), "orig-1", empty)
		if !errors.Is(err, domain.ErrEmptyBody) {
			reportf(t, "ReplySender", "SendReply", "RS3", "ErrEmptyBody wrapper", errString(err))
		}
	})

	t.Run("RS4_cancelled_ctx", func(t *testing.T) {
		rs := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := rs.SendReply(ctx, "orig-1", okMsg)
		if !errors.Is(err, context.Canceled) {
			reportf(t, "ReplySender", "SendReply", "RS4", "context.Canceled", errString(err))
		}
	})
}
