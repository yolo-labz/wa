package whatsmeow

import (
	"testing"

	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
)

func ctx(forwarded bool, score uint32) *waE2E.ContextInfo {
	return &waE2E.ContextInfo{
		IsForwarded:     proto.Bool(forwarded),
		ForwardingScore: proto.Uint32(score),
	}
}

// TestForwardInfoPerVariant — ContextInfo hangs off the message variant,
// not off waE2E.Message, so every variant that can carry one must be read.
// A variant missing from the walk reads as "never forwarded", which is the
// silent-wrong-answer this table exists to prevent.
func TestForwardInfoPerVariant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
	}{
		{"extended text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("fwd"), ContextInfo: ctx(true, 7),
		}}},
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{ContextInfo: ctx(true, 7)}}},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{ContextInfo: ctx(true, 7)}}},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{ContextInfo: ctx(true, 7)}}},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{ContextInfo: ctx(true, 7)}}},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{ContextInfo: ctx(true, 7)}}},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{ContextInfo: ctx(true, 7)}}},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{ContextInfo: ctx(true, 7)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fwd, score := forwardInfo(tc.msg)
			if !fwd {
				t.Errorf("%s: isForwarded = false, want true", tc.name)
			}
			if score != 7 {
				t.Errorf("%s: forwardingScore = %d, want 7", tc.name, score)
			}
		})
	}
}

// TestForwardInfoAbsent — absent means "not marked", never "unknown". A
// plain Conversation cannot hold ContextInfo at all, which is a property of
// the wire format rather than a gap in the walk.
func TestForwardInfoAbsent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
	}{
		{"nil message", nil},
		{"empty message", &waE2E.Message{}},
		{"plain conversation", &waE2E.Message{Conversation: proto.String("oi")}},
		{"variant without context info", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}},
		{"context info present but unset", &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{ContextInfo: &waE2E.ContextInfo{}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fwd, score := forwardInfo(tc.msg)
			if fwd || score != 0 {
				t.Errorf("%s: got (%v, %d), want (false, 0)", tc.name, fwd, score)
			}
		})
	}
}

// TestForwardInfoScoreIndependentOfFlag — the score is carried as a number
// precisely so a consumer can separate a one-hop forward from WhatsApp's
// "forwarded many times" marker (>= 5). Flattening it into the bool would
// destroy that distinction, so a set score must survive on its own.
func TestForwardInfoScoreIndependentOfFlag(t *testing.T) {
	t.Parallel()
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		ContextInfo: &waE2E.ContextInfo{ForwardingScore: proto.Uint32(128)},
	}}
	fwd, score := forwardInfo(msg)
	if fwd {
		t.Error("isForwarded should stay false when only the score is set")
	}
	if score != 128 {
		t.Errorf("forwardingScore = %d, want 128", score)
	}
}
