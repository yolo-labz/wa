// Package transcribe provides secondary adapters implementing
// app.Transcriber. Each adapter shells out to an external binary; all
// respect ctx cancellation and return typed errors.
package transcribe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrBinaryMissing indicates the transcriber binary is not on PATH.
var ErrBinaryMissing = errors.New("transcribe: binary not found on PATH")

// ErrModelMissing indicates the whisper.cpp model file is absent. The
// caller controls which model (base, small, medium); we do not download.
var ErrModelMissing = errors.New("transcribe: model file missing")

// Whispercpp spawns `whisper-cli` (the 2025+ binary name) or its legacy
// `main` alias, both produced by ggml-org/whisper.cpp. It reads 16-bit PCM
// WAV or OGG/Opus. Output is the plain-text transcript.
type Whispercpp struct {
	// Binary is the resolved executable path; defaults to "whisper-cli".
	Binary string
	// ModelPath is the -m argument (e.g. "models/ggml-base.bin").
	ModelPath string
	// Lang overrides the auto-detect language if non-empty. Individual
	// Transcribe calls can override via the lang arg.
	Lang string
	// Threads is the -t arg; 0 falls back to the binary default (nproc).
	Threads int
}

// NewWhispercpp resolves the whisper-cli binary from PATH, falling back to
// the legacy "main" name, and validates the model file exists. Returns
// ErrBinaryMissing / ErrModelMissing with context on failure.
func NewWhispercpp(binary, modelPath string) (*Whispercpp, error) {
	if binary == "" {
		for _, candidate := range []string{"whisper-cli", "main"} {
			if resolved, err := exec.LookPath(candidate); err == nil {
				binary = resolved
				break
			}
		}
		if binary == "" {
			return nil, fmt.Errorf("%w: whisper-cli|main", ErrBinaryMissing)
		}
	} else if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBinaryMissing, binary)
	}
	if modelPath == "" {
		return nil, fmt.Errorf("%w: empty modelPath", ErrModelMissing)
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrModelMissing, modelPath, err)
	}
	return &Whispercpp{Binary: binary, ModelPath: modelPath}, nil
}

// Transcribe implements app.Transcriber.
func (w *Whispercpp) Transcribe(ctx context.Context, path string, lang string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("transcribe: stat %s: %w", path, err)
	}
	effLang := lang
	if effLang == "" {
		effLang = w.Lang
	}
	args := []string{"-m", w.ModelPath, "-f", path, "-otxt", "-of", path + ".transcript", "-nt"}
	if effLang != "" {
		args = append(args, "-l", effLang)
	} else {
		args = append(args, "-l", "auto")
	}
	if w.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(w.Threads))
	}
	var stderr bytes.Buffer
	// #nosec G204 -- w.Binary is operator-configured (whisper-cli path vetted
	// at construction via exec.LookPath); args are literal flags plus the
	// caller-vetted media path from the CAS.
	cmd := exec.CommandContext(ctx, w.Binary, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("transcribe: whisper-cli: %w: %s", err, stderr.String())
	}
	textPath := path + ".transcript.txt"
	// #nosec G304 -- transcript is written by whisper-cli to a derived path
	// of the caller-vetted CAS input; the adapter owns this read.
	raw, err := os.ReadFile(textPath)
	if err != nil {
		return "", "", fmt.Errorf("transcribe: read transcript %s: %w", textPath, err)
	}
	_ = os.Remove(textPath)
	detected := effLang
	if detected == "" || detected == "auto" {
		detected = detectLangFromStderr(stderr.String())
	}
	return strings.TrimSpace(string(raw)), detected, nil
}

// detectLangFromStderr parses whisper.cpp's "auto-detected language: xx"
// log line. Returns "" when absent.
func detectLangFromStderr(stderr string) string {
	const marker = "auto-detected language:"
	idx := strings.Index(stderr, marker)
	if idx < 0 {
		return ""
	}
	rest := stderr[idx+len(marker):]
	rest = strings.TrimLeft(rest, " \t")
	end := strings.IndexAny(rest, " \t\n")
	if end <= 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
