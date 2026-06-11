//go:build darwin

package transcribe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/execguard"
)

// Hear adapts the Darwin-only `hear` CLI (sveinbjornt/hear — Speech
// framework wrapper). It accepts any audio format CoreAudio reads, so no
// pre-conversion is required for WhatsApp .ogg/.oga payloads.
type Hear struct {
	// Binary is the resolved executable path; defaults to "hear".
	Binary string
	// Lang is a BCP-47 tag forwarded via `-l`. Empty ⇒ system default.
	Lang string
}

// NewHear resolves the hear binary on PATH.
func NewHear(binary string) (*Hear, error) {
	if binary == "" {
		binary = "hear"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBinaryMissing, binary)
	}
	// 018 audit SEC-08: refuse a binary another local principal could
	// swap under us (group/world-writable or foreign-owned).
	if err := execguard.Verify(resolved); err != nil {
		return nil, fmt.Errorf("transcribe: %w", err)
	}
	return &Hear{Binary: resolved}, nil
}

// Transcribe implements app.Transcriber.
func (h *Hear) Transcribe(ctx context.Context, path string, lang string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("transcribe: stat %s: %w", path, err)
	}
	effLang := lang
	if effLang == "" {
		effLang = h.Lang
	}
	args := []string{"-i", path, "-s"}
	if effLang != "" {
		args = append(args, "-l", effLang)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, h.Binary, args...) //nolint:gosec // G204: h.Binary is operator-configured transcriber path; path arg is daemon-owned media cache
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("transcribe: hear: %w: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), effLang, nil
}
