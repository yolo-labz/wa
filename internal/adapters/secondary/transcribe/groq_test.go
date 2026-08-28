package transcribe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		// Live 28/08/2026: a real voice note sniffs as application/ogg, the
		// CAS suffix becomes .ogx (IANA multiplexed Ogg), and Groq answered
		// 400 — allowed suffixes are concrete containers. .ogx must map to
		// .ogg; every other concrete suffix passes through untouched.
		"/home/pedro/.cache/wa/media/sha256/ab/cafe0123.ogx": "audio.ogg",
		"/home/pedro/.cache/wa/media/sha256/ab/cafe0123.OGX": "audio.ogg",
		"/tmp/voice.ogg":  "audio.ogg",
		"/tmp/voice.opus": "audio.opus",
	}
	for in, want := range cases {
		if got := uploadName(in); got != want {
			t.Errorf("uploadName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGroqTranscribeMultipartFilename proves the two wire properties
// end-to-end through Transcribe: a CAS path whose suffix is ".ogx" is
// uploaded as audio.ogg (Groq rejects .ogx with a 400), and no byte of
// the local path or content hash reaches the request body (SEC-09).
func TestGroqTranscribeMultipartFilename(t *testing.T) {
	var gotName, rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rawBody = string(body)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 1 {
			t.Errorf("file parts: got %d, want 1", len(files))
		} else {
			gotName = files[0].Filename
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"ok","language":"pt"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "ca", "fe0123456789abcdef.ogx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("OggS-not-really"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	g := &Groq{APIKey: "sk_test", Endpoint: srv.URL, Model: DefaultGroqModel, Client: srv.Client()}
	text, lang, err := g.Transcribe(context.Background(), path, "")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "ok" || lang != "pt" {
		t.Fatalf("Transcribe = (%q, %q), want (ok, pt)", text, lang)
	}
	if gotName != "audio.ogg" {
		t.Fatalf("multipart filename = %q, want audio.ogg", gotName)
	}
	if strings.Contains(rawBody, dir) || strings.Contains(rawBody, "fe0123456789abcdef") {
		t.Fatal("request body leaks the local path or content hash")
	}
}
