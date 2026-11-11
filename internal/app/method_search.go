package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// messagesSearchParams is the JSON-RPC params for "messages.search".
type messagesSearchParams struct {
	Phrase string   `json:"phrase,omitempty"`
	All    []string `json:"all,omitempty"`
	Any    []string `json:"any,omitempty"`
	Chat   string   `json:"chat,omitempty"`
	Since  int64    `json:"since,omitempty"`
	Until  int64    `json:"until,omitempty"`
	Limit  int      `json:"limit,omitempty"`
	// Mode selects BM25 (default), hybrid, or vector-only retrieval
	// per FR-024/FR-101. Feature 017 T3-12.
	Mode string `json:"mode,omitempty"`
}

// hintEnableEmbeddings is the constant hint surfaced when the caller
// requests a mode that requires embeddings but the flag is off. Worded
// per spec.md §FR-024 and the agent UX research in §B-04.
const hintEnableEmbeddings = "Enable embeddings feature for semantic recall"

// searchHitView is the on-wire shape of a single BM25 hit. Snippet is
// channel-wrapped because it is attacker-controlled message content.
type searchHitView struct {
	MessageID string  `json:"messageId"`
	Chat      string  `json:"chat"`
	Sender    string  `json:"sender,omitempty"`
	Snippet   string  `json:"snippet"`
	TS        int64   `json:"ts"`
	Score     float64 `json:"score"`
}

// handleMessagesSearch implements "messages.search". Supports three modes
// per FR-024/FR-101: bm25 (default), hybrid (RRF fusion), vector-only.
// Hybrid with embeddings flag off degrades to BM25 and returns a hint.
// Explicit vector with flag off returns -32113 embeddings_disabled.
// The (mode × flag × filter) decision table lives in validateSearchParams
// and runSearch.
func (d *Dispatcher) handleMessagesSearch(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p messagesSearchParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	mode, err := d.validateSearchParams(&p)
	if err != nil {
		return nil, err
	}
	chat, err := parseSearchChat(p.Chat)
	if err != nil {
		return nil, err
	}
	searcher, ok := d.history.(MessageSearcher)
	if !ok {
		return nil, ErrMethodNotFound
	}
	sq := SearchQuery{
		Phrase: p.Phrase,
		All:    p.All,
		Any:    p.Any,
		Chat:   chat,
		Since:  p.Since,
		Until:  p.Until,
	}
	hits, effMode, hint, err := d.runSearch(ctx, searcher, sq, mode, p.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]searchHitView, 0, len(hits))
	for _, h := range hits {
		out = append(out, searchHitView{
			MessageID: string(h.MessageID),
			Chat:      jidString(h.Chat),
			Sender:    jidString(h.Sender),
			Snippet:   ChannelWrap(h.Snippet, h.Chat, h.Sender, h.TS.Unix()),
			TS:        h.TS.Unix(),
			Score:     h.Score,
		})
	}
	result := struct {
		Hits []searchHitView `json:"hits"`
		Mode string          `json:"mode"`
		Hint string          `json:"hint,omitempty"`
	}{Hits: out, Mode: effMode, Hint: hint}
	return marshalResult(result)
}

// validateSearchParams checks the query shape, clamps p.Limit to [1,100]
// (default 20) and returns the normalised mode. -32113 fires only for the
// explicit vector-only path with the embeddings flag off.
func (d *Dispatcher) validateSearchParams(p *messagesSearchParams) (string, error) {
	if p.Phrase == "" && len(p.All) == 0 && len(p.Any) == 0 {
		return "", ErrInvalidParams
	}
	if rejectsRawFTS(p.Phrase) || anyRejectsRawFTS(p.All) || anyRejectsRawFTS(p.Any) {
		return "", ErrInvalidParams
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.Limit > 100 {
		p.Limit = 100
	}
	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	if mode == "" {
		mode = "bm25"
	}
	switch mode {
	case "bm25", "hybrid", "vector":
	default:
		return "", ErrInvalidParams
	}
	// Hybrid degrades silently to BM25 with a hint.
	if mode == "vector" && !d.features.Embeddings {
		return "", ErrEmbeddingsDisabled
	}
	return mode, nil
}

// parseSearchChat parses the optional chat filter; the zero JID means "any".
func parseSearchChat(raw string) (domain.JID, error) {
	if raw == "" {
		return domain.JID{}, nil
	}
	j, err := domain.Parse(raw)
	if err != nil {
		return domain.JID{}, ErrInvalidJID
	}
	return j, nil
}

// runSearch picks the search backend for mode and returns the hits, the
// effective mode reported on the wire, and an optional user-visible hint.
func (d *Dispatcher) runSearch(ctx context.Context, searcher MessageSearcher, sq SearchQuery, mode string, limit int) ([]SearchHit, string, string, error) {
	var (
		hits    []SearchHit
		err     error
		hint    string
		effMode = mode
	)
	switch {
	case mode == "hybrid" && !d.features.Embeddings:
		// Flag off: silent degrade with user-visible hint.
		hits, err = searcher.SearchBM25(ctx, sq, limit)
		effMode = "bm25"
		hint = hintEnableEmbeddings
	case (mode == "hybrid" || mode == "vector") && d.hybrid != nil:
		h := *d.hybrid
		h.Searcher = searcher
		hits, err = h.Search(ctx, sq, limit)
	case mode == "vector":
		// Flag on but no hybrid searcher wired on the daemon — report
		// unsupported rather than silently returning BM25 under a
		// vector-only label.
		return nil, "", "", ErrEmbeddingsDisabled
	default:
		hits, err = searcher.SearchBM25(ctx, sq, limit)
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("messages.search: %w", err)
	}
	return hits, effMode, hint, nil
}

// rejectsRawFTS returns true when s contains raw FTS5 operators that the
// public search surface MUST NOT accept (NEAR, column filters, prefixes).
// FR-020 requires the socket validator to keep FTS syntax server-side only.
func rejectsRawFTS(s string) bool {
	if s == "" {
		return false
	}
	bad := []string{" NEAR ", " AND ", " OR ", " NOT ", ":", "^", "\""}
	up := strings.ToUpper(s)
	for _, b := range bad {
		if strings.Contains(up, strings.ToUpper(b)) {
			return true
		}
	}
	return false
}

func anyRejectsRawFTS(ss []string) bool {
	return slices.ContainsFunc(ss, rejectsRawFTS)
}
