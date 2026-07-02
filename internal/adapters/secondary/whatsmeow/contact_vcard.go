package whatsmeow

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// contactVCardContent extracts shared contact cards from a message as
// (body, mediaType, caption) content — the vCard text goes in body with
// media_type=text/vcard so the existing read-path SELECTs (which already
// select body+media_type+caption) surface it with no schema change; caption
// carries the human display name. Covers both contactMessage (single card)
// and contactsArrayMessage (multi-contact share, vCards joined by newline).
// ok=false for every other message type.
//
// #281 landed this for the history-sync decoder only
// (extractHistorySyncMessageContent); PR #284 shares it with the live
// inbound path (extractBodyAndMedia) so contacts received while the daemon
// is connected stop persisting with body="". (Pre-fix rows keep body="";
// their vCard is still in raw_proto and recoverable via a one-off backfill.)
func contactVCardContent(msg *waE2E.Message) (body, mediaType, caption string, ok bool) {
	if ctc := msg.GetContactMessage(); ctc != nil {
		return ctc.GetVcard(), "text/vcard", ctc.GetDisplayName(), true
	}
	if arr := msg.GetContactsArrayMessage(); arr != nil {
		vcards := make([]string, 0, len(arr.GetContacts()))
		for _, c := range arr.GetContacts() {
			if v := c.GetVcard(); v != "" {
				vcards = append(vcards, v)
			}
		}
		return strings.Join(vcards, "\n"), "text/vcard", arr.GetDisplayName(), true
	}
	return "", "", "", false
}
