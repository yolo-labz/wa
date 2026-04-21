package memory

import (
	"context"
	"fmt"
	"os"

	"github.com/yolo-labz/wa/internal/app"
)

// Transcriber is the in-memory (canned) implementation of app.Transcriber.
// It stats the file to honour TR1 "typed error on missing path" and returns
// a fixed transcript otherwise. Tests that need deterministic output can
// seed Canned.
type Transcriber struct {
	Canned   string
	Language string
}

// NewTranscriber returns a Transcriber that emits (Canned, Language, nil)
// for any existing path.
func NewTranscriber() *Transcriber {
	return &Transcriber{Canned: "memory transcript", Language: "en"}
}

// Transcribe implements app.Transcriber.
func (s *Transcriber) Transcribe(ctx context.Context, path string, lang string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("transcriber: stat %s: %w", path, err)
	}
	detected := s.Language
	if lang != "" {
		detected = lang
	}
	return s.Canned, detected, nil
}

// Compile-time check.
var _ app.Transcriber = (*Transcriber)(nil)
