package porttest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

func testMessageSender(t *testing.T, factory Factory) {
	t.Helper()
	to := domain.MustJID("5511999999999")

	t.Run("MS1_happy", func(t *testing.T) {
		a := factory(t)
		id, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"})
		if err != nil {
			reportf(t, "MessageSender", "Send", "MS1", "nil error", err.Error())
		}
		if id.IsZero() {
			reportf(t, "MessageSender", "Send", "MS1", "non-zero MessageID", "zero")
		}
	})

	t.Run("MS2_empty_body", func(t *testing.T) {
		a := factory(t)
		id, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: ""})
		if !errors.Is(err, domain.ErrEmptyBody) {
			reportf(t, "MessageSender", "Send", "MS2", "err wrapping ErrEmptyBody", errString(err))
		}
		if !id.IsZero() {
			reportf(t, "MessageSender", "Send", "MS2", "zero MessageID", id.String())
		}
	})

	t.Run("MS3_too_large", func(t *testing.T) {
		a := factory(t)
		big := strings.Repeat("x", domain.MaxTextBytes+1)
		id, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: big})
		if !errors.Is(err, domain.ErrMessageTooLarge) {
			reportf(t, "MessageSender", "Send", "MS3", "err wrapping ErrMessageTooLarge", errString(err))
		}
		if !id.IsZero() {
			reportf(t, "MessageSender", "Send", "MS3", "zero MessageID", id.String())
		}
	})

	t.Run("MS4_cancelled_ctx", func(t *testing.T) {
		a := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := a.Send(ctx, domain.TextMessage{Recipient: to, Body: "hi"})
		if !errors.Is(err, context.Canceled) {
			reportf(t, "MessageSender", "Send", "MS4", "context.Canceled", errString(err))
		}
	})

	t.Run("MS5_concurrent", func(t *testing.T) {
		a := factory(t)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"})
				if err != nil {
					reportf(t, "MessageSender", "Send", "MS5", "nil error", err.Error())
				}
			}()
		}
		wg.Wait()
	})

	t.Run("MS6_media_missing_path", func(t *testing.T) {
		a := factory(t)
		// Provide a syntactically valid but nonexistent path. The
		// in-memory adapter may choose to skip filesystem validation;
		// as long as either the validation error or a not-found error
		// is returned, the contract holds.
		_, err := a.Send(context.Background(), domain.MediaMessage{
			Recipient: to,
			Path:      "/nonexistent/definitely-not-a-real-path-" + time.Now().Format("20060102150405"),
			Mime:      "image/png",
		})
		if err == nil {
			reportf(t, "MessageSender", "Send", "MS6", "non-nil error", "nil")
		}
	})
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
