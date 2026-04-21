package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yolo-labz/wa/internal/domain"
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
// Branch count tracks the (mode × flag × filter) decision table from
// the spec; refactor would fragment error-code mapping.
//
//nolint:gocyclo // decision table, one branch per spec row
func (d *Dispatcher) handleMessagesSearch(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p messagesSearchParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Phrase == "" && len(p.All) == 0 && len(p.Any) == 0 {
		return nil, ErrInvalidParams
	}
	if rejectsRawFTS(p.Phrase) || anyRejectsRawFTS(p.All) || anyRejectsRawFTS(p.Any) {
		return nil, ErrInvalidParams
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
		return nil, ErrInvalidParams
	}
	// -32113 fires only for the explicit vector-only path with the flag
	// off. Hybrid degrades silently to BM25 with a hint.
	if mode == "vector" && !d.features.Embeddings {
		return nil, ErrEmbeddingsDisabled
	}
	var chat domain.JID
	if p.Chat != "" {
		j, err := domain.Parse(p.Chat)
		if err != nil {
			return nil, ErrInvalidJID
		}
		chat = j
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
	var (
		hits    []SearchHit
		err     error
		hint    string
		effMode = mode
	)
	switch {
	case mode == "hybrid" && !d.features.Embeddings:
		// Flag off: silent degrade with user-visible hint.
		hits, err = searcher.SearchBM25(ctx, sq, p.Limit)
		effMode = "bm25"
		hint = hintEnableEmbeddings
	case (mode == "hybrid" || mode == "vector") && d.hybrid != nil:
		h := *d.hybrid
		h.Searcher = searcher
		hits, err = h.Search(ctx, sq, p.Limit)
	case mode == "vector":
		// Flag on but no hybrid searcher wired on the daemon — report
		// unsupported rather than silently returning BM25 under a
		// vector-only label.
		return nil, ErrEmbeddingsDisabled
	default:
		hits, err = searcher.SearchBM25(ctx, sq, p.Limit)
	}
	if err != nil {
		return nil, fmt.Errorf("messages.search: %w", err)
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
	mode = effMode
	result := struct {
		Hits []searchHitView `json:"hits"`
		Mode string          `json:"mode"`
		Hint string          `json:"hint,omitempty"`
	}{Hits: out, Mode: mode, Hint: hint}
	return marshalResult(result)
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
	for _, s := range ss {
		if rejectsRawFTS(s) {
			return true
		}
	}
	return false
}
