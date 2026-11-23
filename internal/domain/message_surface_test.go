package domain

import (
	"log/slog"
	"strings"
	"testing"
)

// Every variant answers the whole Message surface, logs through LogValue
// with its variant name and recipient, and keeps long bodies out of the log.
func TestMessageSurface_AllVariants(t *testing.T) {
	t.Parallel()
	for _, m := range allVariants() {
		t.Run(m.Variant(), func(t *testing.T) {
			t.Parallel()
			m.isMessage()
			_ = m.Content()
			_ = m.To()
			if v, ok := m.(interface{ Validate() error }); ok {
				_ = v.Validate() // zero values are invalid for most variants; the call must not panic
			}
			lv, ok := m.(slog.LogValuer)
			if !ok {
				t.Fatalf("%T does not implement slog.LogValuer", m)
			}
			got := lv.LogValue()
			if got.Kind() != slog.KindGroup {
				t.Fatalf("LogValue kind = %v, want group", got.Kind())
			}
			var typ string
			for _, a := range got.Group() {
				if a.Key == "type" {
					typ = a.Value.String()
				}
			}
			if typ != m.Variant() {
				t.Errorf("logged type %q, want %q", typ, m.Variant())
			}
		})
	}
}

func TestPreview_TruncatesLongBodies(t *testing.T) {
	t.Parallel()
	if got := preview("short"); got != "short" {
		t.Errorf("preview(short) = %q", got)
	}
	long := strings.Repeat("a", previewLen+50)
	got := preview(long)
	if !strings.HasSuffix(got, "…") || len(got) > previewLen+len("…") {
		t.Errorf("preview(long) = %d bytes %q, want %d bytes plus an ellipsis", len(got), got[:10], previewLen)
	}
}

// Validate of the simple variants: a recipient and the one required field
// pass; a missing recipient, an empty payload or an out-of-range pin fail.
func TestSimpleVariants_Validate(t *testing.T) {
	t.Parallel()
	to := MustJID("5511999999999@s.whatsapp.net")
	cases := []struct {
		name    string
		ok, bad interface{ Validate() error }
	}{
		{"audio", AudioMessage{Recipient: to, Path: "/a.ogg"}, AudioMessage{Recipient: to}},
		{"video", VideoMessage{Recipient: to, Path: "/a.mp4"}, VideoMessage{Recipient: to}},
		{"document", DocumentMessage{Recipient: to, Path: "/a.pdf"}, DocumentMessage{Recipient: to}},
		{"sticker", StickerMessage{Recipient: to, Path: "/a.webp"}, StickerMessage{Recipient: to}},
		{"contact", ContactCard{Recipient: to, VCard: "BEGIN:VCARD"}, ContactCard{Recipient: to}},
		{"location", LocationPin{Recipient: to, Latitude: -8, Longitude: -35}, LocationPin{Recipient: to, Latitude: 91}},
		{"location lon", LocationPin{Recipient: to}, LocationPin{Recipient: to, Longitude: 181}},
		{"unknown", UnknownMessage{Recipient: to}, UnknownMessage{}},
	}
	for _, c := range cases {
		if err := c.ok.Validate(); err != nil {
			t.Errorf("%s: valid value refused: %v", c.name, err)
		}
		if err := c.bad.Validate(); err == nil {
			t.Errorf("%s: invalid value accepted", c.name)
		}
	}
}

// The media log line names the payload source and never the payload.
func TestMediaMessage_LogValueSource(t *testing.T) {
	t.Parallel()
	to := MustJID("5511999999999@s.whatsapp.net")
	for want, m := range map[string]MediaMessage{
		"path":   {Recipient: to, Path: "/a.png"},
		"bytes":  {Recipient: to, Bytes: []byte("x")},
		"sha256": {Recipient: to, SHA256: strings.Repeat("a", 64)},
		"none":   {Recipient: to},
	} {
		var got string
		for _, a := range m.LogValue().Group() {
			if a.Key == "source" {
				got = a.Value.String()
			}
		}
		if got != want {
			t.Errorf("source = %q, want %q", got, want)
		}
	}
	b := ButtonReplyMessage{Recipient: to, Kind: ButtonReplyTemplate}.LogValue()
	var kind string
	for _, a := range b.Group() {
		if a.Key == "kind" {
			kind = a.Value.String()
		}
	}
	if kind != "template_button" {
		t.Errorf("button kind = %q, want template_button", kind)
	}
}
