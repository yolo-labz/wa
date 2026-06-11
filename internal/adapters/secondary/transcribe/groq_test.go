package transcribe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGroqOptInGated(t *testing.T) {
	_, err := NewGroq("")
	if !errors.Is(err, ErrGroqDisabled) {
		t.Fatalf("NewGroq(\"\"): want ErrGroqDisabled, got %v", err)
	}
}

func TestLoadGroqKeyEnv(t *testing.T) {
	t.Setenv("WA_GROQ_API_KEY", "sk_test_env")
	got, err := LoadGroqKey("")
	if err != nil {
		t.Fatalf("LoadGroqKey: %v", err)
	}
	if got != "sk_test_env" {
		t.Fatalf("got %q want sk_test_env", got)
	}
}

func TestLoadGroqKeyFile(t *testing.T) {
	t.Setenv("WA_GROQ_API_KEY", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "groq.env")
	body := "# comment\nWA_GROQ_API_KEY=sk_from_file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadGroqKey(path)
	if err != nil {
		t.Fatalf("LoadGroqKey: %v", err)
	}
	if got != "sk_from_file" {
		t.Fatalf("got %q want sk_from_file", got)
	}
}

func TestLoadGroqKeyMissing(t *testing.T) {
	t.Setenv("WA_GROQ_API_KEY", "")
	got, err := LoadGroqKey("/does/not/exist")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

// TestGroqUploadNameLeaksNoPath — 018 audit SEC-09: the multipart
// filename sent to api.groq.com must never carry the local filesystem
// path, only an anonymous name with a format-hint extension.
func TestGroqUploadNameLeaksNoPath(t *testing.T) {
	cases := map[string]string{
		"/home/pedro/.cache/wa/media/sha256/ab/cdef0123":     "audio.ogg",
		"/home/pedro/.cache/wa/media/sha256/ab/cdef0123.oga": "audio.oga",
		"/tmp/voice.wav": "audio.wav",
		"relative/blob":  "audio.ogg",
	}
	for in, want := range cases {
		if got := uploadName(in); got != want {
			t.Errorf("uploadName(%q) = %q, want %q", in, got, want)
		}
	}
}
