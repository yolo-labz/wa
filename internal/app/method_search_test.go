package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeSearcher is a minimal in-memory HistoryStore + MessageSearcher used
// to exercise the messages.search mode dispatch paths without pulling in
// the sqlitehistory adapter.
type fakeSearcher struct {
	hits []app.SearchHit
}

func (f *fakeSearcher) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	return nil, nil
}

func (f *fakeSearcher) SearchBM25(ctx context.Context, q app.SearchQuery, limit int) ([]app.SearchHit, error) {
	return f.hits, nil
}

func newSearchDispatcher(t *testing.T, features app.FeatureFlags, hits []app.SearchHit) *app.Dispatcher {
	t.Helper()
	d := app.NewDispatcher(app.DispatcherConfig{
		Events:         blockingEvents{},
		Allowlist:      allowAll{},
		Audit:          nopAudit{},
		History:        &fakeSearcher{hits: hits},
		Features:       features,
		Profile:        "default",
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// messagesSearchResult mirrors the anonymous struct returned by
// handleMessagesSearch for wire decoding.
type messagesSearchResult struct {
	Hits []struct {
		MessageID string  `json:"messageId"`
		Score     float64 `json:"score"`
	} `json:"hits"`
	Mode string `json:"mode"`
	Hint string `json:"hint,omitempty"`
}

// TestMessagesSearchBm25Default — default mode is bm25 with no hint and
// the mode field present in the result. Back-compat for feature 009 callers.
func TestMessagesSearchBm25Default(t *testing.T) {
	t.Parallel()
	d := newSearchDispatcher(t, app.FeatureFlags{},
		[]app.SearchHit{{MessageID: "m1", TS: time.Unix(1_700_000_000, 0)}})
	raw, err := d.Handle(context.Background(), "messages.search",
		json.RawMessage(`{"phrase":"hello"}`))
	if err != nil {
		t.Fatalf("messages.search bm25: %v", err)
	}
	var r messagesSearchResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Mode != "bm25" || r.Hint != "" || len(r.Hits) != 1 {
		t.Fatalf("unexpected: %+v", r)
	}
}

// TestModeVectorDisabled32113 — mode:"vector" with flag off returns -32113.
// Matches tasks-tier3.md T3-12 test name.
func TestModeVectorDisabled32113(t *testing.T) {
	t.Parallel()
	d := newSearchDispatcher(t, app.FeatureFlags{Embeddings: false}, nil)
	_, err := d.Handle(context.Background(), "messages.search",
		json.RawMessage(`{"phrase":"hello","mode":"vector"}`))
	if !errors.Is(err, app.ErrEmbeddingsDisabled) {
		t.Fatalf("vector+flag-off: got %v want ErrEmbeddingsDisabled (-32113)", err)
	}
}

// TestModeHybridDegradesWithHint — mode:"hybrid" with flag off falls back
// to BM25 and surfaces a user-visible hint. Matches tasks-tier3.md T3-12.
func TestModeHybridDegradesWithHint(t *testing.T) {
	t.Parallel()
	d := newSearchDispatcher(t, app.FeatureFlags{Embeddings: false},
		[]app.SearchHit{{MessageID: "m1", TS: time.Unix(1_700_000_000, 0)}})
	raw, err := d.Handle(context.Background(), "messages.search",
		json.RawMessage(`{"phrase":"hello","mode":"hybrid"}`))
	if err != nil {
		t.Fatalf("hybrid+flag-off: %v", err)
	}
	var r messagesSearchResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Mode != "bm25" {
		t.Fatalf("expected degraded mode=bm25, got %q", r.Mode)
	}
	if r.Hint == "" {
		t.Fatalf("expected user-visible hint when degrading hybrid, got empty")
	}
	if len(r.Hits) != 1 {
		t.Fatalf("expected BM25 hits to be returned alongside hint, got %d", len(r.Hits))
	}
}

// TestModeUnknownRejects — an unknown mode string produces ErrInvalidParams
// rather than silently defaulting to bm25.
func TestModeUnknownRejects(t *testing.T) {
	t.Parallel()
	d := newSearchDispatcher(t, app.FeatureFlags{}, nil)
	_, err := d.Handle(context.Background(), "messages.search",
		json.RawMessage(`{"phrase":"hello","mode":"magic"}`))
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Fatalf("unknown mode: got %v want ErrInvalidParams", err)
	}
}
