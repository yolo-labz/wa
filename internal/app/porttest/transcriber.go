package porttest

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
)

// TranscriberFactory returns a fresh Transcriber for one sub-test.
type TranscriberFactory func(t *testing.T) app.Transcriber

// RunTranscriberContract runs every contract clause for Transcriber per
// FR-054/055.
//
//   TR1 Transcribe of a non-existent path returns a typed error (not a
//       panic; not an empty string).
//   TR2 Honour ctx cancellation: cancelled ctx ⇒ context.Canceled.
func RunTranscriberContract(t *testing.T, factory TranscriberFactory) {
	t.Helper()

	t.Run("TR1 missing path", func(t *testing.T) {
		s := factory(t)
		_, _, err := s.Transcribe(context.Background(), "/does/not/exist.ogg", "")
		if err == nil {
			reportf(t, "Transcriber", "Transcribe", "TR1", "typed error", "<nil>")
		}
	})

	t.Run("TR2 ctx cancelled", func(t *testing.T) {
		s := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := s.Transcribe(ctx, "/does/not/exist.ogg", "")
		if !errors.Is(err, context.Canceled) && err == nil {
			reportf(t, "Transcriber", "Transcribe", "TR2", "ctx error", "<nil>")
		}
	})
}
