//go:build darwin

package main

import (
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/transcribe"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// newHearTranscriber wraps transcribe.NewHear behind a build tag because the
// underlying CLI is a Darwin-only Speech framework wrapper (sveinbjornt/hear).
// The non-darwin sibling in transcriber_other.go returns an explicit error so
// a misconfigured WA_TRANSCRIBER=hear on linux fails loudly rather than
// silently no-opping (rule 12).
func newHearTranscriber(bin string) (app.Transcriber, error) {
	return transcribe.NewHear(bin)
}
