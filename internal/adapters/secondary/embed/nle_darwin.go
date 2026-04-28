//go:build darwin

package embed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// NLEmbedder is the Apple-platform embedder: it execs a signed Swift
// helper binary that wraps NLEmbedding (sentenceEmbedding). Output is
// a 512-dim vector for the en_US document model.
//
// The adapter is darwin-only. The helper binary is expected to live at
// the configured BinaryPath (default "wa-nle-helper") and speak the
// simple protocol below over stdin/stdout so the adapter never has to
// manage a long-lived child process.
//
// Protocol (one request per invocation):
//
//	stdin:  {"text": "<utf-8 string>"}\n
//	stdout: {"embedding": [f32, ...], "model": "en_US_sentence_v1", "dim": 512}\n
//
// T3-05, FR-102.
type NLEmbedder struct {
	// BinaryPath is the helper binary. Empty resolves via exec.LookPath.
	BinaryPath string
	// Model is the helper's returned identity; defaults to
	// "darwin-nle-sentence-512" if the helper omits it.
	Model string
	// Dim is the declared output dimensionality; defaults to 512.
	Dim int
}

// NewNLEmbedder returns a darwin NLE adapter with en_US sentence defaults.
func NewNLEmbedder() *NLEmbedder {
	return &NLEmbedder{
		BinaryPath: "wa-nle-helper",
		Model:      "darwin-nle-sentence-512",
		Dim:        512,
	}
}

// Info returns the adapter's identity.
func (n *NLEmbedder) Info() app.EmbedderInfo {
	return app.EmbedderInfo{Model: n.Model, Dim: n.Dim}
}

// Detect confirms the Swift helper is on PATH.
func (n *NLEmbedder) Detect() error {
	bin := n.BinaryPath
	if bin == "" {
		bin = "wa-nle-helper"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%w: %s", ErrBinaryMissing, bin)
	}
	return nil
}

// Embed runs the helper once per call. The short-lived exec keeps the
// adapter idempotent under crashes: an abandoned child never survives a
// daemon restart. For higher throughput a future patch can hold a
// persistent helper under a JSON-line protocol; v0.5 starts simple.
func (n *NLEmbedder) Embed(ctx context.Context, text string) (domain.Embedding, error) {
	if strings.TrimSpace(text) == "" {
		return domain.Embedding{}, errors.New("embed/nle: empty text")
	}
	bin := n.BinaryPath
	if bin == "" {
		bin = "wa-nle-helper"
	}
	req, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/nle: marshal: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin) //nolint:gosec // G204: bin is operator-configured NLE helper path, not attacker-controlled
	cmd.Stdin = bytes.NewReader(append(req, '\n'))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/nle: exec: %w (stderr=%q)", err, stderr.String())
	}
	return decodeNLEResponse(bufio.NewReader(&stdout), n.Model, n.Dim)
}

// decodeNLEResponse reads one JSON line from r and returns the
// normalised embedding. Exposed for testing via a fake reader.
func decodeNLEResponse(r *bufio.Reader, fallbackModel string, wantDim int) (domain.Embedding, error) {
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return domain.Embedding{}, fmt.Errorf("embed/nle: read: %w", err)
	}
	var decoded struct {
		Embedding []float32 `json:"embedding"`
		Model     string    `json:"model"`
		Dim       int       `json:"dim"`
	}
	if err := json.Unmarshal(line, &decoded); err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/nle: decode: %w", err)
	}
	if len(decoded.Embedding) == 0 {
		return domain.Embedding{}, errors.New("embed/nle: empty embedding")
	}
	if wantDim > 0 && len(decoded.Embedding) != wantDim {
		return domain.Embedding{}, fmt.Errorf("embed/nle: got dim %d want %d",
			len(decoded.Embedding), wantDim)
	}
	model := decoded.Model
	if model == "" {
		model = fallbackModel
	}
	dim := decoded.Dim
	if dim == 0 {
		dim = len(decoded.Embedding)
	}
	vec := decoded.Embedding
	normaliseInPlace(vec)
	return domain.Embedding{Model: model, Dim: dim, Vec: vec}, nil
}
