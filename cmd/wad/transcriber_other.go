//go:build !darwin

package main

import (
	"errors"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// errHearDarwinOnly fails loudly when an operator sets WA_TRANSCRIBER=hear on
// a non-Darwin host. The hear CLI wraps the Apple Speech framework and has no
// counterpart on linux; falling back to whispercpp silently would violate
// rule 12 (no silent fallbacks).
var errHearDarwinOnly = errors.New("the hear adapter is Darwin-only")

func newHearTranscriber(_ string) (app.Transcriber, error) {
	return nil, errHearDarwinOnly
}
