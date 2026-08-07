package domain

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureMediaRef() MediaRef {
	sum := sha256.Sum256([]byte("hello world"))
	return MediaRef{
		SHA256: sum,
		Mime:   "image/jpeg",
		Size:   1024,
		Ext:    "jpg",
	}
}

func TestMediaRefValidate(t *testing.T) {
	ok := fixtureMediaRef()
	if err := ok.Validate(); err != nil {
		t.Fatalf("good ref rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(r *MediaRef)
		want error
	}{
		{"zero sha", func(r *MediaRef) { r.SHA256 = [32]byte{} }, ErrMediaRef},
		{"empty mime", func(r *MediaRef) { r.Mime = "" }, ErrMediaRef},
		{"zero size", func(r *MediaRef) { r.Size = 0 }, ErrMediaRef},
		{"oversize", func(r *MediaRef) { r.Size = MaxInboundMediaBytes + 1 }, ErrMessageTooLarge},
		{"empty ext", func(r *MediaRef) { r.Ext = "" }, ErrMediaRef},
		{"ext with dot", func(r *MediaRef) { r.Ext = ".jpg" }, ErrMediaRef},
		{"ext with slash", func(r *MediaRef) { r.Ext = "foo/bar" }, ErrMediaRef},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fixtureMediaRef()
			tc.mut(&r)
			err := r.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestMediaRefRelativePath(t *testing.T) {
	r := fixtureMediaRef()
	hex := r.HexSHA256()
	got := r.RelativePath()
	want := filepath.Join(hex[:2], hex[2:]+".jpg")
	if got != want {
		t.Fatalf("RelativePath = %q, want %q", got, want)
	}
	if !strings.Contains(got, string(filepath.Separator)) {
		t.Fatalf("RelativePath %q missing separator", got)
	}
}

func TestMediaObjectMimeMismatch(t *testing.T) {
	r := fixtureMediaRef()
	o := MediaObject{
		Ref:            r,
		Path:           "/tmp/x.jpg",
		MimeAdvertised: "image/jpeg",
		MimeDetected:   "image/jpeg",
		FetchedAt:      1,
		LastAccessAt:   1,
	}
	if err := o.Validate(); err != nil {
		t.Fatalf("good MediaObject rejected: %v", err)
	}
	if o.MimeMismatch() {
		t.Fatalf("unexpected mismatch on matching mimes")
	}
	o.MimeDetected = "application/octet-stream"
	if !o.MimeMismatch() {
		t.Fatalf("expected mismatch on differing mimes")
	}
	o.MimeAdvertised = ""
	if o.MimeMismatch() {
		t.Fatalf("MimeMismatch must be false when sender didn't claim a mime")
	}
}

func TestMediaObjectIsAudio(t *testing.T) {
	cases := []struct {
		name       string
		detected   string
		advertised string
		want       bool
	}{
		{"detected audio mpeg", "audio/mpeg", "", true},
		{"detected audio ogg", "audio/ogg", "", true},
		// WhatsApp voice note: Opus-in-Ogg sniffs as the generic container,
		// advertised carries the real audio type (issue #179).
		{"ogg voice note advertised audio", "application/ogg", "audio/ogg; codecs=opus", true},
		// Same container with no advertised mime must still count as audio.
		{"ogg container no advertised", "application/ogg", "", true},
		// Advertised audio rescues an inconclusive sniff.
		{"octet-stream advertised audio", "application/octet-stream", "audio/mp4", true},
		{"image is not audio", "image/jpeg", "", false},
		{"video mp4 is not audio", "video/mp4", "video/mp4", false},
		{"pdf is not audio", "application/pdf", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := MediaObject{MimeDetected: tc.detected, MimeAdvertised: tc.advertised}
			if got := o.IsAudio(); got != tc.want {
				t.Fatalf("IsAudio(detected=%q, advertised=%q) = %v, want %v",
					tc.detected, tc.advertised, got, tc.want)
			}
		})
	}
}
