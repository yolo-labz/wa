package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// MessageSearcherFactory returns a fresh MessageSearcher for one sub-test.
type MessageSearcherFactory func(t *testing.T) app.MessageSearcher

// RunMessageSearcherContract exercises MessageSearcher (FR-020..024).
//
//	MSR1 Empty query returns a typed error (nil query object has no
//	     Phrase/All/Any — adapters MUST refuse).
//	MSR2 Non-empty Phrase returns a (possibly empty) slice without error.
//	MSR3 limit<=0 is rejected with a typed error.
func RunMessageSearcherContract(t *testing.T, factory MessageSearcherFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("MSR1_empty_query_rejected", func(t *testing.T) {
		s := factory(t)
		_, err := s.SearchBM25(ctx, app.SearchQuery{}, 10)
		if err == nil {
			reportf(t, "MessageSearcher", "SearchBM25", "MSR1", "non-nil error", "nil")
		}
	})

	t.Run("MSR2_phrase_returns_slice", func(t *testing.T) {
		s := factory(t)
		hits, err := s.SearchBM25(ctx, app.SearchQuery{Phrase: "hello"}, 10)
		if err != nil {
			reportf(t, "MessageSearcher", "SearchBM25", "MSR2", "nil error", err.Error())
		}
		if hits == nil {
			reportf(t, "MessageSearcher", "SearchBM25", "MSR2", "non-nil slice", "nil slice")
		}
	})

	t.Run("MSR3_bad_limit_rejected", func(t *testing.T) {
		s := factory(t)
		_, err := s.SearchBM25(ctx, app.SearchQuery{Phrase: "x"}, 0)
		if err == nil {
			reportf(t, "MessageSearcher", "SearchBM25", "MSR3", "non-nil error", "nil")
		}
	})
}
