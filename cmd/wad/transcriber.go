package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/transcribe"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// selectTranscriber resolves WA_TRANSCRIBER into an app.Transcriber and its
// lowercase selector name (the value stamped onto TranscriptRecord.Adapter +
// MediaTranscribedEvent.Adapter so operators can audit which adapter produced
// which row).
//
// Spec 110h FR-001..FR-005. Recognised selectors:
//
//   - ""           → disabled. Returns (nil, "", nil); media.download with
//     transcribe=true on audio/* yields -32115.
//   - "whispercpp" → local whisper.cpp. Reads WHISPER_BIN (defaults to
//     `whisper-cli` on PATH then `main`) + WHISPER_MODEL_PATH.
//   - "hear"       → Darwin Speech framework via sveinbjornt/hear CLI. Reads
//     WA_HEAR_BIN (defaults to `hear` on PATH). Returns an
//     explicit error on non-Darwin builds (no silent fallback,
//     rule 12).
//   - "groq"       → cloud HTTP. Reads WA_GROQ_API_KEY or
//     $XDG_CONFIG_HOME/wa/groq.env (KEY=value).
//
// Unknown selectors return an error so a typo never masquerades as "disabled".
func selectTranscriber(log *slog.Logger) (app.Transcriber, string, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("WA_TRANSCRIBER")))
	if raw == "" {
		return nil, "", nil
	}
	switch raw {
	case "whispercpp":
		t, err := transcribe.NewWhispercpp(
			strings.TrimSpace(os.Getenv("WHISPER_BIN")),
			strings.TrimSpace(os.Getenv("WHISPER_MODEL_PATH")),
		)
		if err != nil {
			return nil, "", fmt.Errorf("WA_TRANSCRIBER=whispercpp: %w", err)
		}
		log.Info("transcriber wired", "adapter", "whispercpp",
			"binary", t.Binary, "model", t.ModelPath)
		return t, "whispercpp", nil
	case "hear":
		t, err := newHearTranscriber(strings.TrimSpace(os.Getenv("WA_HEAR_BIN")))
		if err != nil {
			return nil, "", fmt.Errorf("WA_TRANSCRIBER=hear: %w", err)
		}
		log.Info("transcriber wired", "adapter", "hear")
		return t, "hear", nil
	case "groq":
		key, err := loadGroqKey(log)
		if err != nil {
			return nil, "", fmt.Errorf("WA_TRANSCRIBER=groq: %w", err)
		}
		t, err := transcribe.NewGroq(key)
		if err != nil {
			return nil, "", fmt.Errorf("WA_TRANSCRIBER=groq: %w", err)
		}
		log.Info("transcriber wired", "adapter", "groq", "model", t.Model)
		return t, "groq", nil
	default:
		return nil, "", fmt.Errorf("WA_TRANSCRIBER: unknown adapter %q (want whispercpp|hear|groq)", raw)
	}
}

// loadGroqKey resolves the Groq API key. WA_GROQ_API_KEY wins; falls back to
// $XDG_CONFIG_HOME/wa/groq.env (KEY=value, mode 0600). transcribe.LoadGroqKey
// owns the two-source merge; this wrapper only resolves the secrets path and
// converts "no key found" into a typed error so selectTranscriber surfaces
// the misconfiguration instead of returning a silent nil adapter.
func loadGroqKey(log *slog.Logger) (string, error) {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve XDG_CONFIG_HOME: %w", err)
		}
		cfg = filepath.Join(home, ".config")
	}
	path := filepath.Join(cfg, "wa", "groq.env")
	key, err := transcribe.LoadGroqKey(path)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", errors.New("no API key in WA_GROQ_API_KEY or " + path)
	}
	log.Info("loaded groq api key", "source_path", path)
	return key, nil
}
