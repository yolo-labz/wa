package transcribe

import (
	"errors"
	"testing"
)

func TestWhispercppDetect(t *testing.T) {
	_, err := NewWhispercpp("/totally/missing/binary", "/dev/null")
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("missing binary: want ErrBinaryMissing, got %v", err)
	}
}

func TestWhispercppDetectLang(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"whisper_full_with_state: auto-detected language: pt (p = 0.99)", "pt"},
		{"... auto-detected language: en ...", "en"},
		{"no marker here", ""},
	}
	for _, c := range cases {
		got := detectLangFromStderr(c.in)
		if got != c.want {
			t.Errorf("detectLangFromStderr(%q): got %q want %q", c.in, got, c.want)
		}
	}
}
