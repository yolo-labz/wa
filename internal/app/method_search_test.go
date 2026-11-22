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

// plainHistory is a HistoryStore that cannot search, like the daemon's
// WhatsApp adapter.
type plainHistory struct{}

func (plainHistory) LoadMore(context.Context, domain.JID, domain.MessageID, int) ([]domain.Message, error) {
	return nil, nil
}

// TestMessagesSearchSearcherPort: a History that cannot search no longer
// makes messages.search method-not-found once a Searcher is configured (the
// daemon's case, where History is the WhatsApp adapter); without either it
// still is, and a configured Searcher wins over a searching History.
func TestMessagesSearchSearcherPort(t *testing.T) {
	t.Parallel()
	call := func(cfg app.DispatcherConfig) (json.RawMessage, error) {
		cfg.Events, cfg.Allowlist, cfg.Audit = blockingEvents{}, allowAll{}, nopAudit{}
		cfg.Profile, cfg.SessionCreated = "default", time.Now()
		d := app.NewDispatcher(cfg)
		t.Cleanup(func() { _ = d.Close() })
		return d.Handle(t.Context(), "messages.search", json.RawMessage(`{"phrase":"x"}`))
	}
	ids := func(raw json.RawMessage) string {
		var r messagesSearchResult
		if err := json.Unmarshal(raw, &r); err != nil || len(r.Hits) != 1 {
			t.Fatalf("result %s err %v", raw, err)
		}
		return r.Hits[0].MessageID
	}
	one := func(id string) *fakeSearcher {
		return &fakeSearcher{hits: []app.SearchHit{{MessageID: domain.MessageID(id)}}}
	}

	if _, err := call(app.DispatcherConfig{History: plainHistory{}}); !errors.Is(err, app.ErrMethodNotFound) {
		t.Errorf("no searcher anywhere: err %v, want method not found", err)
	}
	raw, err := call(app.DispatcherConfig{History: plainHistory{}, Searcher: one("port")})
	if err != nil || ids(raw) != "port" {
		t.Errorf("Searcher port: %s %v", raw, err)
	}
	raw, err = call(app.DispatcherConfig{History: one("history"), Searcher: one("port")})
	if err != nil || ids(raw) != "port" {
		t.Errorf("Searcher must win over a searching History: %s %v", raw, err)
	}
	raw, err = call(app.DispatcherConfig{History: one("history")})
	if err != nil || ids(raw) != "history" {
		t.Errorf("History fallback: %s %v", raw, err)
	}
}

// TestMessagesSearchRefusesHostileTerms (p8 L1/L2): a control byte or an
// oversized term list is the caller's mistake (-32602), never a store fault.
func TestMessagesSearchRefusesHostileTerms(t *testing.T) {
	t.Parallel()
	d := newSearchDispatcher(t, app.FeatureFlags{}, nil)
	terms := func(n int) string {
		s := make([]string, n)
		for i := range s {
			s[i] = "w"
		}
		b, _ := json.Marshal(s)
		return string(b)
	}
	refused := map[string]string{
		"NUL phrase":    `{"phrase":"a\u0000b"}`,
		"NUL in all":    `{"all":["ok","a\u0000b"]}`,
		"NUL in any":    `{"any":["a\u0000b"]}`,
		"escape byte":   `{"phrase":"a\u001bb"}`,
		"DEL":           `{"phrase":"a\u007fb"}`,
		"33 terms":      `{"all":` + terms(17) + `,"any":` + terms(16) + `}`,
		"100 all terms": `{"all":` + terms(100) + `}`,
		"100 any terms": `{"any":` + terms(100) + `}`,
	}
	for name, body := range refused {
		if _, err := d.Handle(context.Background(), "messages.search", json.RawMessage(body)); !errors.Is(err, app.ErrInvalidParams) {
			t.Errorf("%s: got %v, want ErrInvalidParams", name, err)
		}
	}
	accepted := map[string]string{
		"32 terms":         `{"all":` + terms(16) + `,"any":` + terms(16) + `}`,
		"tab and newline":  `{"phrase":"a\tb\nc\r"}`,
		"non-ASCII phrase": `{"phrase":"açaí 日本"}`,
	}
	for name, body := range accepted {
		if _, err := d.Handle(context.Background(), "messages.search", json.RawMessage(body)); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}
