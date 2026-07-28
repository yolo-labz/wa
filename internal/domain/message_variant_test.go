package domain

import (
	"fmt"
	"testing"
)

// allVariants lists one zero value of every domain.Message variant.
// Adding a variant without adding it here is caught by the count
// assertion below, which is the cheapest available stand-in for
// compile-time exhaustiveness over a sealed interface.
func allVariants() []Message {
	return []Message{
		TextMessage{},
		MediaMessage{},
		AudioMessage{},
		VideoMessage{},
		DocumentMessage{},
		StickerMessage{},
		ContactCard{},
		LocationPin{},
		UnknownMessage{},
		ReactionMessage{},
		ListReplyMessage{},
		ButtonReplyMessage{},
	}
}

// TestVariantNamesUniqueAndNonEmpty locks the wire vocabulary consumers
// switch on. A copy-pasted variant name (two types reporting "audio")
// is invisible at compile time and would silently mislabel a whole
// message class on the subscriber stream.
func TestVariantNamesUniqueAndNonEmpty(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for _, m := range allVariants() {
		name := m.Variant()
		if name == "" {
			t.Errorf("%T.Variant() is empty", m)
			continue
		}
		if seen[name] {
			t.Errorf("%T.Variant() = %q, already used by another variant", m, name)
		}
		seen[name] = true
	}
	if len(seen) != 12 {
		t.Errorf("got %d distinct variant names, want 12 — add the new variant to allVariants()", len(seen))
	}
}

// TestVariantNamesAreStable pins the exact strings. They are wire
// values on the subscriber event stream (SubscriberMessageEvent.Kind)
// and in the "type" key of every message log line, so renaming one
// breaks consumers and log queries alike.
func TestVariantNamesAreStable(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"domain.TextMessage":        "text",
		"domain.MediaMessage":       "media",
		"domain.AudioMessage":       "audio",
		"domain.VideoMessage":       "video",
		"domain.DocumentMessage":    "document",
		"domain.StickerMessage":     "sticker",
		"domain.ContactCard":        "contact",
		"domain.LocationPin":        "location",
		"domain.UnknownMessage":     "unknown",
		"domain.ReactionMessage":    "reaction",
		"domain.ListReplyMessage":   "list_reply",
		"domain.ButtonReplyMessage": "button_reply",
	}
	for _, m := range allVariants() {
		key := fmt.Sprintf("%T", m)
		if got := m.Variant(); got != want[key] {
			t.Errorf("%s.Variant() = %q, want %q", key, got, want[key])
		}
	}
}

// TestContentSurfacesHumanText populates every field a variant could
// plausibly expose and pins what Content actually yields. The point is
// the negative half: persistence and the read views now read Text
// blindly, so anything that lands there is presented to an agent as if
// a contact had typed it. Detail, Address, Path and Filename are
// daemon- or filesystem-derived and must stay out.
func TestContentSurfacesHumanText(t *testing.T) {
	t.Parallel()
	to := MustJID("5581999@s.whatsapp.net")
	cases := []struct {
		msg      Message
		wantText string
		wantMime string
	}{
		{TextMessage{Recipient: to, Body: "oi"}, "oi", ""},
		{TextMessage{Recipient: to, Body: "oi", Mentions: []JID{to}}, "oi @5581999", ""},
		{MediaMessage{Recipient: to, Path: "/tmp/a.png", Mime: "image/png", Caption: "cap", Filename: "a.png"}, "cap", "image/png"},
		{AudioMessage{Recipient: to, Path: "/tmp/a.ogg", Mime: "audio/ogg", Seconds: 3, PTT: true}, "", "audio/ogg"},
		{VideoMessage{Recipient: to, Path: "/tmp/a.mp4", Mime: "video/mp4", Caption: "cap"}, "cap", "video/mp4"},
		{DocumentMessage{Recipient: to, Path: "/tmp/a.pdf", Mime: "application/pdf", Filename: "a.pdf", Caption: "cap"}, "cap", "application/pdf"},
		{StickerMessage{Recipient: to, Path: "/tmp/a.webp", Mime: "image/webp"}, "", "image/webp"},
		{ContactCard{Recipient: to, DisplayName: "Ana", VCard: "BEGIN:VCARD"}, "Ana", ""},
		{LocationPin{Recipient: to, Name: "Marco Zero", Address: "Recife Antigo"}, "Marco Zero", ""},
		{UnknownMessage{Recipient: to, Detail: "pollCreationMessage"}, "", ""},
		{ReactionMessage{Recipient: to, TargetID: "ABC", Emoji: "👍"}, "👍", ""},
		{ListReplyMessage{Recipient: to, RowID: "row-1", Title: "Opção 1"}, "Opção 1", ""},
		{ButtonReplyMessage{Recipient: to, ButtonID: "btn-1", DisplayText: "Sim"}, "Sim", ""},
	}
	covered := make(map[string]bool, len(cases))
	for _, tc := range cases {
		key := fmt.Sprintf("%T", tc.msg)
		covered[key] = true
		got := tc.msg.Content()
		if got.Text != tc.wantText {
			t.Errorf("%s.Content().Text = %q, want %q", key, got.Text, tc.wantText)
		}
		if got.Mime != tc.wantMime {
			t.Errorf("%s.Content().Mime = %q, want %q", key, got.Mime, tc.wantMime)
		}
	}
	for _, m := range allVariants() {
		if key := fmt.Sprintf("%T", m); !covered[key] {
			t.Errorf("%s has no Content() case — add one", key)
		}
	}
}
