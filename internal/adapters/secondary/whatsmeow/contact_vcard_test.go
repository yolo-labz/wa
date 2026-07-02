package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// #284: extractBodyAndMedia (the LIVE inbound persist path) previously had no
// contact branch — the #281 fix covered only the history-sync decoder — so a
// contact card received while the daemon is connected still persisted with
// body="". Both paths now share contactVCardContent. Table-driven over the
// live path; fictional +1-555-01xx numbers, never real PII.
func TestExtractBodyAndMedia_ContactCards(t *testing.T) {
	const single = "BEGIN:VCARD\nVERSION:3.0\nFN:Live Contact\nTEL;type=CELL:+1-555-0110\nEND:VCARD"
	const alice = "BEGIN:VCARD\nFN:Alice Live\nTEL:+1-555-0111\nEND:VCARD"
	const bob = "BEGIN:VCARD\nFN:Bob Live\nTEL:+1-555-0112\nEND:VCARD"

	cases := []struct {
		name                            string
		msg                             *waE2E.Message
		wantBody, wantType, wantCaption string
	}{
		{
			name: "single contactMessage",
			msg: &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
				DisplayName: proto.String("Live Contact"),
				Vcard:       proto.String(single),
			}},
			wantBody: single, wantType: "text/vcard", wantCaption: "Live Contact",
		},
		{
			name: "contactsArrayMessage joins vCards",
			msg: &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{
				DisplayName: proto.String("2 contacts"),
				Contacts: []*waE2E.ContactMessage{
					{Vcard: proto.String(alice)},
					{Vcard: proto.String(bob)},
				},
			}},
			wantBody: alice + "\n" + bob, wantType: "text/vcard", wantCaption: "2 contacts",
		},
		{
			name:     "plain text unaffected",
			msg:      &waE2E.Message{Conversation: proto.String("hello")},
			wantBody: "hello", wantType: "", wantCaption: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, mediaType, caption := extractBodyAndMedia(&events.Message{Message: tc.msg})
			if body != tc.wantBody || mediaType != tc.wantType || caption != tc.wantCaption {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					body, mediaType, caption, tc.wantBody, tc.wantType, tc.wantCaption)
			}
		})
	}
}
