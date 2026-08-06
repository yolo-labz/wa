package main

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

// TestMediaRowBase_EmitsSenderJID pins the top-level senderJid field. It is
// server-assigned metadata, not sender-authored content, so unlike a caption
// it is safe outside the <channel> envelope — and it is the only field that
// lets a caller tell two participants' attachments apart without regexing the
// envelope's XML. Issue #314.
func TestMediaRowBase_EmitsSenderJID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		isFromMe bool
	}{
		{"inbound", false},
		{"outbound", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := mediaRowBase(sqlitehistory.StoredMessage{
				MessageID: "M-1",
				ChatJID:   "120363000000000000@g.us",
				SenderJID: "558177777777@s.whatsapp.net",
				MediaType: "image/jpeg",
				Caption:   "with a caption",
				IsFromMe:  tc.isFromMe,
			})
			// Both directions: the firewall governs the caption, never the
			// addressing metadata, so senderJid must not go missing on the
			// inbound path where triage actually needs it.
			if w.SenderJID != "558177777777@s.whatsapp.net" {
				t.Fatalf("SenderJID = %q, want the row's sender", w.SenderJID)
			}
		})
	}
}

// TestAppliedFilterNames pins the honesty of the appliedFilters echo. It is
// what lets a client prove its --sender survived the wire instead of assuming
// it: encoding/json drops unknown fields, so a client newer than its daemon
// gets the whole list back with exit 0 and no way to tell. Issue #317.
func TestAppliedFilterNames(t *testing.T) {
	t.Parallel()

	// The "any MIME" pattern is what an unfiltered call carries, so it must
	// NOT be reported as a mediaType filter.
	const anyMIME = "%/%"

	for _, tc := range []struct {
		name      string
		filter    sqlitehistory.MessageFilter
		mediaType string
		want      []string
	}{{
		name:   "unfiltered",
		filter: sqlitehistory.MessageFilter{MediaTypeLike: anyMIME, Limit: 50},
		want:   []string{},
	}, {
		name:      "every narrowing filter",
		mediaType: "audio",
		filter: sqlitehistory.MessageFilter{
			ChatJID: "120363000000000000@g.us", SenderJID: "5581@s.whatsapp.net",
			MediaTypeLike: "audio/%", Caption: "nota",
			Since: 1782864000, Until: 1784073600, Limit: 50,
		},
		want: []string{"chat", "sender", "mediaType", "caption", "since", "until"},
	}, {
		name:   "sender only",
		filter: sqlitehistory.MessageFilter{SenderJID: "5581@lid", MediaTypeLike: anyMIME},
		want:   []string{"sender"},
	}, {
		// limit is not narrowing: dropping it returns fewer rows, never
		// somebody else's, so it is deliberately absent from the echo.
		name:   "limit alone is not a filter",
		filter: sqlitehistory.MessageFilter{MediaTypeLike: anyMIME, Limit: 1},
		want:   []string{},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := appliedFilterNames(tc.filter, tc.mediaType)
			if got == nil {
				t.Fatal("appliedFilterNames returned nil; it must marshal to [] " +
					"so that an ABSENT field can only mean an older daemon")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
