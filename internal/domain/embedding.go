package domain

import "errors"

// Embedding is a dense vector representation of a chunk of text associated
// with a concrete message (FR-101/102). Vectors are float32 to match the
// Go-side embedder adapters; INT8 quantisation is an adapter-local detail.
//
// Dim must equal len(Vec). Model is an opaque string (e.g. "bge-small-384",
// "voyage-3-1024") that callers use to prevent mixing embedding spaces.
type Embedding struct {
	MessageID MessageID
	Model     string
	Dim       int
	Vec       []float32
}

// ErrDimMismatch is returned when Dim does not equal len(Vec) or the caller
// passes a vector whose length contradicts the Embedder's declared Dim.
var ErrDimMismatch = errors.New("embedding: declared dim does not match vector length")

// ErrEmptyModel is returned when an Embedding is constructed without a
// model identifier — embedding spaces are NOT interchangeable.
var ErrEmptyModel = errors.New("embedding: model identifier is required")

// Validate reports structural violations. It does NOT check numerical
// sanity (non-finite elements) — that is an adapter concern when normalising.
func (e Embedding) Validate() error {
	if e.Model == "" {
		return ErrEmptyModel
	}
	if e.Dim != len(e.Vec) {
		return ErrDimMismatch
	}
	if e.MessageID == "" {
		return errors.New("embedding: message id is required")
	}
	return nil
}
