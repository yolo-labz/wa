package sqlitehistory_test

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

// The three filters under test exist so that picking media out of a busy
// group does not degenerate into "fetch everything near this timestamp and
// look at it" — which is how a caller ends up with attachments that were
// never theirs to open. Issue #314.

// ids collects message ids in result order for compact assertions.
func ids(ms []sqlitehistory.StoredMessage) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.MessageID
	}
	return out
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// TestQueryMessagesFiltered_SenderMatchesBothNamespaces is the load-bearing
// one: a group can address participants by phone number or by LID, and a row
// records one in sender_jid with the other in sender_alt_jid (spec 107). A
// filter that only consulted sender_jid would return "no messages" for half
// the corpus — an under-matching sender filter is worse than none, because it
// reads as proof the person sent nothing.
func TestQueryMessagesFiltered_SenderMatchesBothNamespaces(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const (
		group = "120363000000000000@g.us"
		pn    = "5511900000001@s.whatsapp.net"
		lid   = "77712345678901@lid"
		other = "5511900000002@s.whatsapp.net"
	)
	// pn-addressed row: primary = phone, alt = lid.
	if err := s.InsertRaw(ctx, group, pn, "m-pn", 100, "", "image/jpeg", "", "", false, nil, lid, "pn"); err != nil {
		t.Fatalf("InsertRaw pn: %v", err)
	}
	// lid-addressed row from the SAME person: the columns swap.
	if err := s.InsertRaw(ctx, group, lid, "m-lid", 200, "", "image/jpeg", "", "", false, nil, pn, "lid"); err != nil {
		t.Fatalf("InsertRaw lid: %v", err)
	}
	// A third participant that must never appear in either result.
	if err := s.InsertRaw(ctx, group, other, "m-other", 300, "", "image/jpeg", "", "", false, nil, "", "pn"); err != nil {
		t.Fatalf("InsertRaw other: %v", err)
	}

	for _, jid := range []string{pn, lid} {
		got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{SenderJID: jid})
		if err != nil {
			t.Fatalf("QueryMessagesFiltered(%s): %v", jid, err)
		}
		// Newest first, and both rows regardless of which namespace was asked
		// for — the two ids are the same human.
		assertIDs(t, ids(got), []string{"m-lid", "m-pn"})
	}

	got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{SenderJID: other})
	if err != nil {
		t.Fatalf("QueryMessagesFiltered(other): %v", err)
	}
	assertIDs(t, ids(got), []string{"m-other"})
}

// TestQueryMessagesFiltered_CaptionLikeEscapes proves the caption predicate
// treats LIKE metacharacters as literals. Without ESCAPE, a caption search
// for "100%" degrades into "starts with 100 and then anything", which quietly
// widens a filter whose whole purpose is to narrow.
func TestQueryMessagesFiltered_CaptionLikeEscapes(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const chat = "5511900000003@s.whatsapp.net"
	seed := map[string]string{
		"m-pct":    "desconto de 100% hoje",
		"m-plain":  "desconto de 100 reais hoje",
		"m-under":  "snapshot_1 final",
		"m-underX": "snapshotX1 final",
	}
	var ts int64 = 100
	for id, caption := range seed {
		if err := s.InsertRaw(ctx, chat, chat, id, ts, "", "image/jpeg", caption, "", false, nil, "", ""); err != nil {
			t.Fatalf("InsertRaw %s: %v", id, err)
		}
		ts++
	}

	// The caller passes a PLAIN substring; the store owns the wildcards, so
	// these are what a user types, not what reaches SQL.
	cases := []struct {
		name string
		sub  string
		want []string
	}{
		// `%` escaped: matches only the literal percent sign.
		{"literal percent", `100%`, []string{"m-pct"}},
		// `_` escaped: does not act as a single-character wildcard.
		{"literal underscore", `snapshot_1`, []string{"m-under"}},
		// An ordinary substring still matches both percent rows.
		{"plain substring", `desconto`, []string{"m-plain", "m-pct"}},
		// Media rows with an empty caption are never swept in.
		{"no match", `nada`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{Caption: tc.sub})
			if err != nil {
				t.Fatalf("QueryMessagesFiltered: %v", err)
			}
			gotIDs := ids(got)
			if len(gotIDs) != len(tc.want) {
				t.Fatalf("ids = %v, want %v", gotIDs, tc.want)
			}
			// Rows seeded from a map share no meaningful order beyond ts, so
			// compare as a set.
			seen := make(map[string]bool, len(gotIDs))
			for _, id := range gotIDs {
				seen[id] = true
			}
			for _, want := range tc.want {
				if !seen[want] {
					t.Fatalf("ids = %v, want to contain %q", gotIDs, want)
				}
			}
		})
	}
}

// TestQueryMessagesFiltered_CaptionAccentFolding is the #315 fix, and it used
// to assert the opposite. Against a caption reading "Segue nosso catálogo",
// SQLite's ASCII-only LIKE meant both the shouted "CATÁLOGO" and the
// unaccented "catalogo" a phone keyboard produces matched NOTHING — an empty
// result that reads as "no such media" and sends the caller back to picking
// rows out of a busy group by timestamp proximity. Every case below is now a
// match, in both directions: an accented needle finds an unaccented caption
// and vice versa.
func TestQueryMessagesFiltered_CaptionAccentFolding(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const chat = "5511900000004@s.whatsapp.net"
	if err := s.InsertRaw(ctx, chat, chat, "m-cat", 100, "", "image/jpeg", "Segue nosso catálogo", "", false, nil, "", ""); err != nil {
		t.Fatalf("InsertRaw: %v", err)
	}
	// An unaccented caption, to prove the fold runs on BOTH sides rather than
	// just stripping the needle.
	if err := s.InsertRaw(ctx, chat, chat, "m-sao", 101, "", "image/jpeg", "Foto em Sao Paulo", "", false, nil, "", ""); err != nil {
		t.Fatalf("InsertRaw: %v", err)
	}

	for _, tc := range []struct {
		name string
		sub  string
		want string
	}{
		{"exact", "catálogo", "m-cat"},
		{"ascii case up", "Catálogo", "m-cat"},
		{"ascii word shouted", "SEGUE", "m-cat"},
		// The three that used to miss.
		{"accented shouted", "CATÁLOGO", "m-cat"},
		{"accent stripped", "catalogo", "m-cat"},
		{"accent stripped shouted", "CATALOGO", "m-cat"},
		// Accented needle, unaccented caption — the other direction.
		{"accented needle plain caption", "São", "m-sao"},
		{"plain needle plain caption", "sao", "m-sao"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{Caption: tc.sub})
			if err != nil {
				t.Fatalf("QueryMessagesFiltered: %v", err)
			}
			assertIDs(t, ids(got), []string{tc.want})
		})
	}
}

// TestQueryMessagesFiltered_CaptionFoldIsSearchKeyOnly pins that folding is a
// search key and never a display value. If a read path ever started returning
// caption_folded, a caption would come back lowercased and stripped of its
// accents — the daemon would be quietly rewriting what a person actually sent.
func TestQueryMessagesFiltered_CaptionFoldIsSearchKeyOnly(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const chat = "5511900000009@s.whatsapp.net"
	const original = "Segue nosso CATÁLOGO"
	if err := s.InsertRaw(ctx, chat, chat, "m-disp", 100, "", "image/jpeg", original, "", false, nil, "", ""); err != nil {
		t.Fatalf("InsertRaw: %v", err)
	}

	got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{Caption: "catalogo"})
	if err != nil {
		t.Fatalf("QueryMessagesFiltered: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].Caption != original {
		t.Errorf("Caption = %q, want the bytes the sender wrote (%q)", got[0].Caption, original)
	}
}

// TestQueryMessagesFiltered_FromMeTriState pins the WAA-14 direction
// discriminator at the store level: FromMe=nil (unset) returns BOTH
// directions, FromMe=&true only own (outbound) rows, FromMe=&false only
// inbound rows. The three-state contract is load-bearing — a caller that
// omits the param must get the whole list, not silently lose half of it.
func TestQueryMessagesFiltered_FromMeTriState(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const chat = "5511900000005@s.whatsapp.net"
	rows := []struct {
		id     string
		ts     int64
		fromMe bool
	}{
		{"m-own-1", 100, true},
		{"m-in-1", 200, false},
		{"m-own-2", 300, true},
		{"m-in-2", 400, false},
	}
	for _, r := range rows {
		if err := s.InsertRaw(ctx, chat, chat, r.id, r.ts, "", "image/jpeg", "", "", r.fromMe, nil, "", ""); err != nil {
			t.Fatalf("InsertRaw %s: %v", r.id, err)
		}
	}

	// omitted → both directions
	got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{})
	if err != nil {
		t.Fatalf("QueryMessagesFiltered(nil): %v", err)
	}
	assertIDs(t, ids(got), []string{"m-in-2", "m-own-2", "m-in-1", "m-own-1"})

	// true → only own
	trueVal := true
	got, err = s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{FromMe: &trueVal})
	if err != nil {
		t.Fatalf("QueryMessagesFiltered(true): %v", err)
	}
	assertIDs(t, ids(got), []string{"m-own-2", "m-own-1"})

	// false → only inbound
	falseVal := false
	got, err = s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{FromMe: &falseVal})
	if err != nil {
		t.Fatalf("QueryMessagesFiltered(false): %v", err)
	}
	assertIDs(t, ids(got), []string{"m-in-2", "m-in-1"})
}

// TestQueryMessagesFiltered_SenderAndWindowCompose checks the new predicates
// AND together with the pre-existing ones rather than replacing them.
func TestQueryMessagesFiltered_SenderAndWindowCompose(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const (
		group = "120363000000000001@g.us"
		alice = "5511900000004@s.whatsapp.net"
		bob   = "5511900000005@s.whatsapp.net"
	)
	rows := []struct {
		id     string
		sender string
		ts     int64
		mime   string
	}{
		{"a-early", alice, 100, "image/jpeg"},
		{"a-mid", alice, 200, "image/jpeg"},
		{"a-mid-audio", alice, 250, "audio/ogg"},
		{"a-late", alice, 300, "image/jpeg"},
		{"b-mid", bob, 200, "image/jpeg"},
	}
	for _, r := range rows {
		if err := s.InsertRaw(ctx, group, r.sender, r.id, r.ts, "", r.mime, "", "", false, nil, "", ""); err != nil {
			t.Fatalf("InsertRaw %s: %v", r.id, err)
		}
	}

	got, err := s.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{
		ChatJID:       group,
		SenderJID:     alice,
		MediaTypeLike: "image/%",
		Since:         150,
		Until:         280,
	})
	if err != nil {
		t.Fatalf("QueryMessagesFiltered: %v", err)
	}
	assertIDs(t, ids(got), []string{"a-mid"})
}
