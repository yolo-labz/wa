package sqlitehistory

import (
	"slices"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func TestStoreIsAMessageSearcher(t *testing.T) {
	porttest.RunMessageSearcherContract(t, func(t *testing.T) app.MessageSearcher {
		st, _ := openRevokeTestStore(t)
		return st
	})
}

const (
	bmChatA = "15551230001@s.whatsapp.net"
	bmChatB = "15551230009@s.whatsapp.net"
)

func seedBM25(t *testing.T) *Store {
	t.Helper()
	st, ctx := openRevokeTestStore(t)
	rows := []StoredMessage{
		{ChatJID: bmChatA, SenderJID: bmChatA, MessageID: "A-1", Timestamp: 100, Body: "invoice paid, invoice sent, invoice again"},
		{ChatJID: bmChatA, SenderJID: bmChatA, MessageID: "A-2", Timestamp: 200, Body: "the invoice is due, see Café menu"},
		{ChatJID: bmChatB, SenderJID: bmChatB, MessageID: "B-1", Timestamp: 300, Body: "invoice from the other chat"},
		{ChatJID: bmChatA, SenderJID: bmChatA, MessageID: "A-DEAD", Timestamp: 400, Body: "invoice revoked for everyone"},
		{ChatJID: bmChatA, SenderJID: bmChatA, MessageID: "A-GONE", Timestamp: 500, Body: "invoice deleted for me"},
		{ChatJID: bmChatA, SenderJID: bmChatA, MessageID: "A-TXT", Timestamp: 600, Body: "unrelated words: a:b NEAR(x) \"q\" *"},
	}
	if err := st.Insert(ctx, rows); err != nil {
		t.Fatal(err)
	}
	chat := domain.MustJID(bmChatA)
	for id, scope := range map[string]domain.RevokeScope{"A-DEAD": domain.RevokeEveryone, "A-GONE": domain.RevokeSelf} {
		if err := st.StampRevoke(ctx, chat, domain.MessageID(id), scope, 999); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func bmIDs(hits []app.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = string(h.MessageID)
	}
	return out
}

// wantIDs compares as a set: the order is BM25's and is pinned by
// TestSearchBM25_RanksAndFillsTheHit.
func wantIDs(t *testing.T, what string, hits []app.SearchHit, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	got := bmIDs(hits)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s: hits %v, want %v", what, got, want)
	}
}

func TestSearchBM25_RanksAndFillsTheHit(t *testing.T) {
	st := seedBM25(t)
	hits, err := st.SearchBM25(t.Context(), app.SearchQuery{Phrase: "invoice"}, 10)
	// A-1 repeats the term, so it outranks A-2 and B-1; revoked rows are absent.
	if err != nil || len(hits) != 3 || hits[0].MessageID != "A-1" {
		t.Fatalf("hits=%v err=%v, want A-1 first of 3", bmIDs(hits), err)
	}
	h := hits[0]
	if h.Chat.String() != bmChatA || h.Sender.String() != bmChatA || !h.TS.Equal(time.Unix(100, 0)) || h.Score <= hits[1].Score {
		t.Errorf("hit fields wrong: %+v (next score %v)", h, hits[1].Score)
	}
	if h.Snippet == "" || h.Snippet[0] == '<' || h.Snippet == "…" {
		t.Errorf("snippet %q must be the plain matched text", h.Snippet)
	}
}

func TestSearchBM25_SkipsRevokedForEveryone(t *testing.T) {
	st := seedBM25(t)
	for _, phrase := range []string{"revoked", "deleted"} {
		hits, err := st.SearchBM25(t.Context(), app.SearchQuery{Phrase: phrase}, 10)
		wantIDs(t, phrase, hits, err) // everyone (positive stamp) and for me (negative) alike
	}
}

func TestSearchBM25_Filters(t *testing.T) {
	st := seedBM25(t)
	ctx := t.Context()
	hits, err := st.SearchBM25(ctx, app.SearchQuery{Phrase: "invoice", Chat: domain.MustJID(bmChatB)}, 10)
	wantIDs(t, "chat", hits, err, "B-1")
	hits, err = st.SearchBM25(ctx, app.SearchQuery{Phrase: "invoice", Since: 200}, 10)
	wantIDs(t, "since is inclusive", hits, err, "A-2", "B-1")
	hits, err = st.SearchBM25(ctx, app.SearchQuery{Phrase: "invoice", Until: 200}, 10)
	wantIDs(t, "until is inclusive", hits, err, "A-1", "A-2")
	hits, err = st.SearchBM25(ctx, app.SearchQuery{Phrase: "invoice", Since: 200, Until: 200}, 10)
	wantIDs(t, "since=until", hits, err, "A-2")
	hits, err = st.SearchBM25(ctx, app.SearchQuery{Phrase: "invoice"}, 1)
	wantIDs(t, "limit", hits, err, "A-1")
}

func TestSearchBM25_AllAndAny(t *testing.T) {
	st := seedBM25(t)
	ctx := t.Context()
	hits, err := st.SearchBM25(ctx, app.SearchQuery{All: []string{"invoice", "due"}}, 10)
	wantIDs(t, "all", hits, err, "A-2")
	hits, err = st.SearchBM25(ctx, app.SearchQuery{Phrase: "invoice", Any: []string{"paid", "other"}}, 10)
	wantIDs(t, "phrase and any", hits, err, "A-1", "B-1")
	hits, err = st.SearchBM25(ctx, app.SearchQuery{Any: []string{"zzz", "café"}}, 10)
	wantIDs(t, "any, accent-folded", hits, err, "A-2")
	if _, err := st.SearchBM25(ctx, app.SearchQuery{Phrase: "  ", All: []string{""}}, 10); err == nil {
		t.Error("blank terms must be an empty query")
	}
}

// Caller text is never FTS5 syntax: operators, quotes, column filters and
// prefix stars search for themselves instead of erroring or widening.
func TestSearchBM25_QueryTextIsNotSyntax(t *testing.T) {
	st := seedBM25(t)
	for _, phrase := range []string{`invoice" OR "x`, `NEAR(x)`, `body:invoice`, `a:b`, `inv*`, `"`, `invoice AND`, `(`} {
		hits, err := st.SearchBM25(t.Context(), app.SearchQuery{Phrase: phrase}, 10)
		if err != nil {
			t.Errorf("phrase %q: %v", phrase, err)
		}
		for _, h := range hits {
			if h.MessageID != "A-TXT" {
				t.Errorf("phrase %q matched %s: operator text widened the query", phrase, h.MessageID)
			}
		}
	}
	hits, err := st.SearchBM25(t.Context(), app.SearchQuery{Phrase: `NEAR(x)`}, 10)
	wantIDs(t, "literal NEAR", hits, err, "A-TXT")
}
