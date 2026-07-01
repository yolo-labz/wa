package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"
)

// #281: a shared contact (contactMessage) previously had no branch in
// extractHistorySyncMessageContent, so it was stored with body="" and the
// phone number (vCard TEL) was silently dropped from history/export/search.
// The vCard must now surface in body with media_type=text/vcard.
func TestExtractHistorySyncMessageContent_ContactMessage(t *testing.T) {
	const vcard = "BEGIN:VCARD\nVERSION:3.0\nFN:Dra Dora\nTEL;type=CELL:+55 81 9172-2479\nEND:VCARD"
	wmInfo := &waWeb.WebMessageInfo{
		Message: &waE2E.Message{
			ContactMessage: &waE2E.ContactMessage{
				DisplayName: proto.String("Dra Dora"),
				Vcard:       proto.String(vcard),
			},
		},
	}

	body, mediaType, caption := extractHistorySyncMessageContent(wmInfo)
	if body != vcard {
		t.Errorf("body = %q, want the vCard payload", body)
	}
	if mediaType != "text/vcard" {
		t.Errorf("mediaType = %q, want text/vcard", mediaType)
	}
	if caption != "Dra Dora" {
		t.Errorf("caption = %q, want the contact display name", caption)
	}
}
