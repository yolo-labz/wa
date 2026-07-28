package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"
)

// #281: shared contacts (contactMessage / contactsArrayMessage) previously had
// no branch in the history-sync content projection, so they were stored with
// body="" and the vCard (phone number) was dropped from history/export/search.
// The vCard(s) must now surface in body with media_type=text/vcard. Test data
// uses reserved fictional numbers (+1-555-01xx) — never real PII.

func TestHistorySyncContent_ContactMessage(t *testing.T) {
	const vcard = "BEGIN:VCARD\nVERSION:3.0\nFN:Test Contact\nTEL;type=CELL:+1-555-0100\nEND:VCARD"
	wmInfo := &waWeb.WebMessageInfo{
		Message: &waE2E.Message{
			ContactMessage: &waE2E.ContactMessage{
				DisplayName: proto.String("Test Contact"),
				Vcard:       proto.String(vcard),
			},
		},
	}

	body, mediaType, caption := messageContent(wmInfo.GetMessage())
	if body != vcard {
		t.Errorf("body = %q, want the vCard payload", body)
	}
	if mediaType != "text/vcard" {
		t.Errorf("mediaType = %q, want text/vcard", mediaType)
	}
	if caption != "Test Contact" {
		t.Errorf("caption = %q, want the contact display name", caption)
	}
}

func TestHistorySyncContent_ContactsArrayMessage(t *testing.T) {
	const v1 = "BEGIN:VCARD\nFN:Alice Example\nTEL:+1-555-0101\nEND:VCARD"
	const v2 = "BEGIN:VCARD\nFN:Bob Example\nTEL:+1-555-0102\nEND:VCARD"
	wmInfo := &waWeb.WebMessageInfo{
		Message: &waE2E.Message{
			ContactsArrayMessage: &waE2E.ContactsArrayMessage{
				DisplayName: proto.String("2 contacts"),
				Contacts: []*waE2E.ContactMessage{
					{Vcard: proto.String(v1)},
					{Vcard: proto.String(v2)},
				},
			},
		},
	}

	body, mediaType, caption := messageContent(wmInfo.GetMessage())
	if body != v1+"\n"+v2 {
		t.Errorf("body = %q, want both vCards joined", body)
	}
	if mediaType != "text/vcard" {
		t.Errorf("mediaType = %q, want text/vcard", mediaType)
	}
	if caption != "2 contacts" {
		t.Errorf("caption = %q, want the array display name", caption)
	}
}
