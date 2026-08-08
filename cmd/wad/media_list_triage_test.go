package main

import (
	"encoding/json"
	"slices"
	"strings"
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

// TestMediaRowBase_EmitsFromMe pins the WAA-14 direction discriminator on
// every wire row: fromMe must be present in BOTH directions. It is NOT
// omitempty on purpose — a false (inbound) row that dropped the field
// would be indistinguishable from a pre-WAA-14 daemon, and the whole point
// of the field is telling the bilu-bridge audio poll which side of the
// chat a voice note came from without a sentIds heuristic.
func TestMediaRowBase_EmitsFromMe(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		isFromMe bool
		want     bool
	}{
		{"outbound", true, true},
		{"inbound", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := mediaRowBase(sqlitehistory.StoredMessage{
				MessageID: "M-1",
				ChatJID:   "120363000000000000@g.us",
				SenderJID: "558177777777@s.whatsapp.net",
				MediaType: "audio/ogg",
				IsFromMe:  tc.isFromMe,
			})
			if w.FromMe != tc.want {
				t.Fatalf("FromMe = %v, want %v", w.FromMe, tc.want)
			}
			// The wire must carry fromMe explicitly (no omitempty), so a
			// marshalled row always has the key — the regression the field
			// exists to catch.
			raw, err := json.Marshal(w)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(raw), `"fromMe":`) {
				t.Fatalf("marshalled row %s lacks fromMe key (omitempty dropped it?)", raw)
			}
		})
	}
}

// TestMediaRowBase_CaptionWrappingUnchanged pins the FR-005a firewall
// against the WAA-14 change: adding fromMe must not alter which captions
// get wrapped. Inbound captions (attacker-controllable) stay inside the
// <channel> envelope with raw Caption empty; outbound captions pass raw.
// The flag is set BOTH ways in this test to prove it is not consulted by
// the firewall switch.
func TestMediaRowBase_CaptionWrappingUnchanged(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		isFromMe bool
		caption  string
		wantChan bool // whether Channel should be set
		wantCap  string
	}{
		{"inbound with caption wrapped", false, "attacker text", true, ""},
		{"inbound empty caption untouched", false, "", false, ""},
		{"outbound caption passes raw", true, "my own caption", false, "my own caption"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := mediaRowBase(sqlitehistory.StoredMessage{
				MessageID: "M-1",
				ChatJID:   "120363000000000000@g.us",
				SenderJID: "558177777777@s.whatsapp.net",
				MediaType: "image/jpeg",
				Caption:   tc.caption,
				IsFromMe:  tc.isFromMe,
			})
			if (w.Channel != "") != tc.wantChan {
				t.Fatalf("Channel set = %v, want %v (firewall must ignore fromMe)", w.Channel != "", tc.wantChan)
			}
			if w.Caption != tc.wantCap {
				t.Fatalf("Caption = %q, want %q", w.Caption, tc.wantCap)
			}
		})
	}
}

// TestMediaListParams_FromMeTriState pins the wire-level tri-state
// contract: the JSON param "fromMe" must propagate to the store filter's
// FromMe *bool exactly — true → &true, false → &false, absent → nil
// (either direction). The pointer is what keeps "false" distinguishable
// from "unset": a plain bool would silently filter every legacy caller
// to inbound-only.
func TestMediaListParams_FromMeTriState(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		raw     string
		want    *bool
		wantIn  []string // appliedFilterNames must include these
		wantOut string   // appliedFilterNames must NOT include this
	}{
		{"true", `{"fromMe":true}`, boolPtr(true), []string{"fromMe"}, ""},
		{"false", `{"fromMe":false}`, boolPtr(false), []string{"fromMe"}, ""},
		{"absent", `{}`, nil, []string{}, "fromMe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var p mediaListParams
			if err := json.Unmarshal([]byte(tc.raw), &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if (p.FromMe == nil) != (tc.want == nil) {
				t.Fatalf("FromMe nil-ness = %v, want %v", p.FromMe == nil, tc.want == nil)
			}
			if tc.want != nil && (p.FromMe == nil || *p.FromMe != *tc.want) {
				t.Fatalf("FromMe = %v, want %v", p.FromMe, *tc.want)
			}
			// The filter built by the handler must carry it through, and the
			// honest-filters echo must report it.
			f := sqlitehistory.MessageFilter{FromMe: p.FromMe}
			got := appliedFilterNames(f, "")
			if tc.wantIn != nil {
				for _, want := range tc.wantIn {
					if !slices.Contains(got, want) {
						t.Fatalf("appliedFilterNames = %v, want to contain %q", got, want)
					}
				}
			}
			if tc.wantOut != "" && slices.Contains(got, tc.wantOut) {
				t.Fatalf("appliedFilterNames = %v, must NOT contain %q", got, tc.wantOut)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

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
