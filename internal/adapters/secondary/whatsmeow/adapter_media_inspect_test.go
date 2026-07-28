package whatsmeow

import (
	"context"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// TestInspectMedia_PTTDiscriminatesVoiceNotes pins the audio metadata
// media.list reports. WhatsApp advertises a recorded voice note and an
// attached audio file with the same audio/ogg MIME and both carry a
// duration, so PTT is the only field that separates them — an agent
// deciding whether to transcribe cannot do it on mime or duration alone.
// The live-event translator already captures PTT (translate_event.go) and
// the send path already sets it (upload.go); the stored-proto read path
// silently dropped it, which is the asymmetry this test closes.
func TestInspectMedia_PTTDiscriminatesVoiceNotes(t *testing.T) {
	t.Parallel()

	audio := func(ptt bool) []byte {
		t.Helper()
		blob, err := proto.Marshal(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype:   proto.String("audio/ogg; codecs=opus"),
			FileLength: proto.Uint64(4096),
			Seconds:    proto.Uint32(7),
			PTT:        proto.Bool(ptt),
		}})
		if err != nil {
			t.Fatalf("marshal audio proto: %v", err)
		}
		return blob
	}
	image, err := proto.Marshal(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype:   proto.String("image/jpeg"),
		FileLength: proto.Uint64(2048),
	}})
	if err != nil {
		t.Fatalf("marshal image proto: %v", err)
	}

	tests := []struct {
		name    string
		raw     []byte
		wantPTT bool
	}{
		{"voice note", audio(true), true},
		{"attached audio file", audio(false), false},
		// Non-audio media has no PTT field at all; the nil-safe getter
		// chain must report false rather than panic.
		{"image", image, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newMediaAdapterForTest(t, &mediaHistory{chatJID: "55119@s.whatsapp.net", rawProto: tt.raw})

			info, present, err := m.InspectMedia(context.Background(), "3ADC343BAD95E8A638CE")
			if err != nil {
				t.Fatalf("InspectMedia: %v", err)
			}
			if !present {
				t.Fatal("present = false, want true: the proto carries media")
			}
			if info.PTT != tt.wantPTT {
				t.Errorf("PTT = %v, want %v", info.PTT, tt.wantPTT)
			}
		})
	}
}

// TestInspectMedia_AudioReportsDurationAndMime guards the fields PTT sits
// beside: a voice note is only actionable if the caller also learns how
// long it is and what codec it is in, and all three come off the same
// proto read.
func TestInspectMedia_AudioReportsDurationAndMime(t *testing.T) {
	t.Parallel()

	blob, err := proto.Marshal(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype:   proto.String("audio/ogg; codecs=opus"),
		FileLength: proto.Uint64(4096),
		Seconds:    proto.Uint32(7),
		PTT:        proto.Bool(true),
	}})
	if err != nil {
		t.Fatalf("marshal audio proto: %v", err)
	}
	m := newMediaAdapterForTest(t, &mediaHistory{chatJID: "55119@s.whatsapp.net", rawProto: blob})

	info, present, err := m.InspectMedia(context.Background(), "3ADC343BAD95E8A638CE")
	if err != nil {
		t.Fatalf("InspectMedia: %v", err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if info.DurationSeconds != 7 {
		t.Errorf("DurationSeconds = %d, want 7", info.DurationSeconds)
	}
	if info.AdvertisedMime != "audio/ogg; codecs=opus" {
		t.Errorf("AdvertisedMime = %q, want %q", info.AdvertisedMime, "audio/ogg; codecs=opus")
	}
	if info.AdvertisedSize != 4096 {
		t.Errorf("AdvertisedSize = %d, want 4096", info.AdvertisedSize)
	}
}
