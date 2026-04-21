package domain

import (
	"errors"
	"testing"
)

func TestEmbeddingValidate(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		e := Embedding{MessageID: MessageID("m-1"), Model: "bge-small-384", Dim: 3, Vec: []float32{0.1, 0.2, 0.3}}
		if err := e.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
	t.Run("missing model", func(t *testing.T) {
		e := Embedding{MessageID: MessageID("m-1"), Dim: 1, Vec: []float32{1}}
		if err := e.Validate(); !errors.Is(err, ErrEmptyModel) {
			t.Fatalf("got %v, want ErrEmptyModel", err)
		}
	})
	t.Run("dim mismatch", func(t *testing.T) {
		e := Embedding{MessageID: MessageID("m-1"), Model: "x", Dim: 2, Vec: []float32{1}}
		if err := e.Validate(); !errors.Is(err, ErrDimMismatch) {
			t.Fatalf("got %v, want ErrDimMismatch", err)
		}
	})
	t.Run("missing message id", func(t *testing.T) {
		e := Embedding{Model: "x", Dim: 0, Vec: []float32{}}
		if err := e.Validate(); err == nil {
			t.Fatalf("want error for missing MessageID")
		}
	})
}
