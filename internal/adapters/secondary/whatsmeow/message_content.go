package whatsmeow

import (
	"strconv"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// messageContent projects a whatsmeow message onto the three columns the
// history schema stores for it: body, media_type, caption.
//
// One function, two callers: the live inbound path
// (persistInboundMessage) and the history-sync decoder
// (persistOneMessage). Each used to carry its own copy of this switch, and
// the copies drifted — PR #282 taught the history decoder to read contact
// cards, and every contact card received while the daemon was connected
// kept persisting with body="" until PR #283 noticed and patched the live
// copy too. The projection is one decision about one wire format; it gets
// one implementation.
//
// media_type holds a real registered MIME type or nothing at all. Media
// variants supply their own via GetMimetype(); contact cards get
// text/vcard because that type genuinely describes a vCard payload. No
// variant gets an invented x- type — a location pin has no registered
// MIME, so it carries an empty media_type and identifies itself by the
// geo: URI in body instead.
func messageContent(msg *waE2E.Message) (body, mediaType, caption string) {
	if msg == nil {
		return "", "", ""
	}
	if c := msg.GetConversation(); c != "" {
		return c, "", ""
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText(), "", ""
	}
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption(), img.GetMimetype(), img.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption(), doc.GetMimetype(), doc.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption(), vid.GetMimetype(), vid.GetCaption()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "", aud.GetMimetype(), ""
	}
	// Stickers carry a real mimetype (image/webp) and no text at all. The
	// mimetype alone is enough to persist and render the row; without this
	// branch a sticker fell through to ("", "", "") and the on-demand
	// history decoder dropped it as "nothing renderable".
	if stk := msg.GetStickerMessage(); stk != nil {
		return "", stk.GetMimetype(), ""
	}
	if body, mt, capt, ok := contactVCardContent(msg); ok {
		return body, mt, capt
	}
	if loc := msg.GetLocationMessage(); loc != nil {
		return locationContent(loc)
	}
	return "", "", ""
}

// locationContent renders a location pin as (body, mediaType, caption).
//
// body is an RFC 5870 geo: URI — the coordinates are the only field a pin
// always has, and the URI form is both greppable and directly openable by
// a map client, which a bare "lat, lon" pair is not. The human label
// (name, or the street address when the sender attached no name) goes in
// caption, matching how contact cards put the machine payload in body and
// the display name in caption.
//
// mediaType stays empty on purpose: no registered MIME describes a
// location pin, and inventing an x- type would break the invariant that
// this column holds real MIME types or nothing.
func locationContent(loc *waE2E.LocationMessage) (body, mediaType, caption string) {
	lat := strconv.FormatFloat(loc.GetDegreesLatitude(), 'f', -1, 64)
	lon := strconv.FormatFloat(loc.GetDegreesLongitude(), 'f', -1, 64)
	label := loc.GetName()
	if label == "" {
		label = loc.GetAddress()
	}
	return "geo:" + lat + "," + lon, "", label
}
