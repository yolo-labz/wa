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

// TestCaptionLikePattern escapes LIKE metacharacters. The wire param is a
// plain substring on purpose: if a client could pass "%" the server would
// happily turn a narrowing filter into a full dump.
func TestCaptionLikePattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"catalogo", "%catalogo%"},
		{"100%", `%100\%%`},
		{"snapshot_1", `%snapshot\_1%`},
		{`back\slash`, `%back\\slash%`},
		// A caller who types "%" gets a search for a literal percent sign,
		// not a match-everything wildcard.
		{"%", `%\%%`},
	}
	for _, tc := range cases {
		if got := captionLikePattern(tc.in); got != tc.want {
			t.Errorf("captionLikePattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
