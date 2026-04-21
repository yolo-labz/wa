package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultGroqEndpoint is the v1 Whisper-compatible transcription endpoint.
const DefaultGroqEndpoint = "https://api.groq.com/openai/v1/audio/transcriptions"

// DefaultGroqModel is the canonical cost/quality ratio (April 2026).
const DefaultGroqModel = "whisper-large-v3-turbo"

// ErrGroqDisabled is returned when the Groq adapter is asked to run
// without an API key. Opt-in is explicit per FR-055 and SG-04.
var ErrGroqDisabled = errors.New("transcribe: groq cloud transcription disabled (no API key)")

// Groq is the cloud transcription adapter. The API key is read from the
// WA_GROQ_API_KEY env var OR $XDG_CONFIG_HOME/wa/groq.env
// (KEY=value format, mode 0600). Loader is explicit at adapter
// construction time — never lazy.
type Groq struct {
	APIKey   string
	Endpoint string
	Model    string
	Client   *http.Client
}

// NewGroq returns a Groq adapter or ErrGroqDisabled when no key resolves.
func NewGroq(apiKey string) (*Groq, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("%w", ErrGroqDisabled)
	}
	return &Groq{
		APIKey:   apiKey,
		Endpoint: DefaultGroqEndpoint,
		Model:    DefaultGroqModel,
		Client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// LoadGroqKey reads WA_GROQ_API_KEY env var first; falls back to the
// KEY=value contents of secretsPath (e.g. $XDG_CONFIG_HOME/wa/groq.env).
// Returns "" (no error) when neither source is configured — callers map
// that to ErrGroqDisabled.
func LoadGroqKey(secretsPath string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("WA_GROQ_API_KEY")); v != "" {
		return v, nil
	}
	if secretsPath == "" {
		return "", nil
	}
	// #nosec G304 -- secretsPath is an operator-configured path
	// ($XDG_CONFIG_HOME/wa/<profile>/groq.env); adapter reads its own secret.
	raw, err := os.ReadFile(secretsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("transcribe: read %s: %w", secretsPath, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == "WA_GROQ_API_KEY" {
			return strings.TrimSpace(val), nil
		}
	}
	return "", nil
}

// Transcribe implements app.Transcriber.
func (g *Groq) Transcribe(ctx context.Context, path string, lang string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	// #nosec G304 -- path is the caller-vetted CAS media path
	// ($XDG_CACHE_HOME/wa/media/sha256/...); content-addressed, adapter-owned.
	f, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("transcribe: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", path)
	if err != nil {
		return "", "", fmt.Errorf("transcribe: form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", "", fmt.Errorf("transcribe: copy: %w", err)
	}
	_ = mw.WriteField("model", g.Model)
	_ = mw.WriteField("response_format", "verbose_json")
	if lang != "" {
		_ = mw.WriteField("language", lang)
	}
	if err := mw.Close(); err != nil {
		return "", "", fmt.Errorf("transcribe: close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.Endpoint, &body)
	if err != nil {
		return "", "", fmt.Errorf("transcribe: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := g.Client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("transcribe: groq request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", fmt.Errorf("transcribe: groq %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var payload struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", fmt.Errorf("transcribe: decode: %w", err)
	}
	detected := payload.Language
	if detected == "" {
		detected = lang
	}
	return strings.TrimSpace(payload.Text), detected, nil
}
