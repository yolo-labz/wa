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
