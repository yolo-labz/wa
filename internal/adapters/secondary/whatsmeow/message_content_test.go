package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// TestMessageContentVariants pins the projection for every variant the
// history schema stores. Stickers and location pins are the regression:
// both fell through to ("", "", ""), which persisted a blank row on the
// live path and made the on-demand decoder skip the message outright as
// "nothing renderable".
func TestMessageContentVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                            string
		msg                             *waE2E.Message
		wantBody, wantType, wantCaption string
	}{
		{
			name:     "sticker carries its mimetype and no text",
			msg:      &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}},
			wantType: "image/webp",
		},
		{
			name: "location renders a geo URI with the name as label",
			msg: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
				DegreesLatitude:  proto.Float64(-8.0476),
				DegreesLongitude: proto.Float64(-34.877),
				Name:             proto.String("Marco Zero"),
				Address:          proto.String("Praça Rio Branco, Recife"),
			}},
			wantBody: "geo:-8.0476,-34.877", wantCaption: "Marco Zero",
		},
		{
			name: "location with no name falls back to the address",
			msg: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
				DegreesLatitude:  proto.Float64(0),
				DegreesLongitude: proto.Float64(0),
				Address:          proto.String("Null Island"),
			}},
			wantBody: "geo:0,0", wantCaption: "Null Island",
		},
		{
			name: "bare coordinates still produce a renderable body",
			msg: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
				DegreesLatitude:  proto.Float64(51.5),
				DegreesLongitude: proto.Float64(-0.12),
			}},
			wantBody: "geo:51.5,-0.12",
		},
		{
			name:     "conversation text is unchanged",
			msg:      &waE2E.Message{Conversation: proto.String("hello")},
			wantBody: "hello",
		},
		{
			name: "image keeps caption in both body and caption",
			msg: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
				Mimetype: proto.String("image/jpeg"),
				Caption:  proto.String("look"),
			}},
			wantBody: "look", wantType: "image/jpeg", wantCaption: "look",
		},
		{
			name: "audio has a mimetype and no text",
			msg: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
				Mimetype: proto.String("audio/ogg; codecs=opus"),
			}},
			wantType: "audio/ogg; codecs=opus",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, mediaType, caption := messageContent(tc.msg)
			if body != tc.wantBody || mediaType != tc.wantType || caption != tc.wantCaption {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					body, mediaType, caption, tc.wantBody, tc.wantType, tc.wantCaption)
			}
		})
	}
}

// TestMessageContentNilMessage guards the two call sites that hand this
// function a pointer straight off the wire: a nil inner message must
// project to empty strings, not panic the inbound goroutine.
func TestMessageContentNilMessage(t *testing.T) {
	t.Parallel()
	body, mediaType, caption := messageContent(nil)
	if body != "" || mediaType != "" || caption != "" {
		t.Errorf("nil message projected to (%q, %q, %q), want all empty", body, mediaType, caption)
	}
}

// TestMessageContentIsRenderableForStoredVariants is the property the two
// fixed variants exist to satisfy. routeOnDemandResponse drops any
// message whose projection is empty in BOTH body and media_type, so a
// variant that projects to nothing is a variant the user never sees in
// `wa sync force` output — silently, with no error anywhere.
func TestMessageContentIsRenderableForStoredVariants(t *testing.T) {
	t.Parallel()

	stored := map[string]*waE2E.Message{
		"text":     {Conversation: proto.String("hi")},
		"image":    {ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}},
		"video":    {VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4")}},
		"audio":    {AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg")}},
		"document": {DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf")}},
		"sticker":  {StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}},
		"contact":  {ContactMessage: &waE2E.ContactMessage{Vcard: proto.String("BEGIN:VCARD\nEND:VCARD")}},
		"location": {LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(1), DegreesLongitude: proto.Float64(2)}},
	}

	for name, msg := range stored {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body, mediaType, _ := messageContent(msg)
			if body == "" && mediaType == "" {
				t.Errorf("%s projects to no body and no media type — the on-demand decoder drops it", name)
			}
		})
	}
}
