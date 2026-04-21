// Package embed provides secondary adapters implementing app.Embedder.
// Each adapter speaks a distinct transport (local HTTP, cloud HTTP, exec
// helper) but returns a normalised float32 vector so the pipeline and
// vector index stay transport-agnostic.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// ErrBinaryMissing is returned when a required local binary is not on
// PATH. Shared across local-exec adapters (llama.cpp, darwin NLE).
var ErrBinaryMissing = errors.New("embed: binary not found on PATH")

// ErrUnhealthy is returned when a background embedder process is not
// reachable on its expected endpoint.
var ErrUnhealthy = errors.New("embed: health check failed")

// LlamaCpp is the local-exec embedder: it spawns (or talks to an
// externally-spawned) `llama-server --embedding` and issues HTTP POSTs
// to /embedding. Default model is BGE-small GGUF quantised to Q4_K_M
// which produces 384-dim vectors. FR-102, T3-03.
//
// The adapter does NOT own the server lifecycle in v0.5 — we expect an
// external launchd/systemd unit, a Homebrew service, or a `llama-server`
// invocation the user has already started. Detect() verifies the binary
// exists; Health() verifies the endpoint responds. This split keeps the
// adapter testable without spawning a child process.
type LlamaCpp struct {
	// Endpoint is the full URL of the /embedding route. Defaults to
	// http://127.0.0.1:8080/embedding when empty.
	Endpoint string
	// Model identifies the embedding model tag stored with each vector.
	// Callers MUST NOT mix vectors from two different Models in the
	// same VectorIndex. Defaults to "bge-small-384".
	Model string
	// Dim is the declared output dimensionality. Defaults to 384.
	Dim int
	// HTTP is the transport used for /embedding calls. Nil means a new
	// http.Client{Timeout: 30s} is used per-call.
	HTTP *http.Client
}

// NewLlamaCpp constructs a LlamaCpp adapter with BGE-small-384 defaults.
func NewLlamaCpp() *LlamaCpp {
	return &LlamaCpp{
		Endpoint: "http://127.0.0.1:8080/embedding",
		Model:    "bge-small-384",
		Dim:      384,
	}
}

// Info returns the adapter's identity.
func (l *LlamaCpp) Info() app.EmbedderInfo {
	return app.EmbedderInfo{Model: l.Model, Dim: l.Dim}
}

// Detect returns nil when the `llama-server` binary is present on PATH.
// It intentionally does NOT probe the Endpoint — that is Health()'s job.
func (l *LlamaCpp) Detect() error {
	if _, err := exec.LookPath("llama-server"); err != nil {
		return fmt.Errorf("%w: llama-server", ErrBinaryMissing)
	}
	return nil
}

// Health issues a GET /health against the server and returns nil on 200.
// The /health route is the standard llama.cpp server liveness probe.
func (l *LlamaCpp) Health(ctx context.Context) error {
	endpoint := l.healthURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("embed/llamacpp: health request: %w", err)
	}
	resp, err := l.client().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnhealthy, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s", ErrUnhealthy, resp.Status)
	}
	return nil
}

// Embed posts {"content": text} to the /embedding endpoint and returns
// an L2-normalised float32 vector.
func (l *LlamaCpp) Embed(ctx context.Context, text string) (domain.Embedding, error) {
	if strings.TrimSpace(text) == "" {
		return domain.Embedding{}, errors.New("embed/llamacpp: empty text")
	}
	body, err := json.Marshal(map[string]string{"content": text})
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/llamacpp: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.endpoint(), bytes.NewReader(body))
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/llamacpp: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client().Do(req)
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/llamacpp: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return domain.Embedding{}, fmt.Errorf("embed/llamacpp: http %d: %s", resp.StatusCode, string(raw))
	}
	vec, err := parseLlamaResponse(resp.Body)
	if err != nil {
		return domain.Embedding{}, err
	}
	if len(vec) != l.Dim {
		return domain.Embedding{}, fmt.Errorf("embed/llamacpp: got dim %d want %d", len(vec), l.Dim)
	}
	normaliseInPlace(vec)
	return domain.Embedding{Model: l.Model, Dim: l.Dim, Vec: vec}, nil
}

// parseLlamaResponse accepts both shapes llama-server has shipped:
//
//	{"embedding": [f32, ...]}            // single-doc legacy shape
//	[{"embedding": [[f32, ...]]}]        // batch-wrapped 2026 shape
func parseLlamaResponse(r io.Reader) ([]float32, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("embed/llamacpp: read: %w", err)
	}
	// Legacy single-doc shape.
	var single struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.Unmarshal(raw, &single); err == nil && len(single.Embedding) > 0 {
		return single.Embedding, nil
	}
	// Batch-wrapped shape (array of objects with matrix embedding).
	var batch []struct {
		Embedding [][]float32 `json:"embedding"`
	}
	if err := json.Unmarshal(raw, &batch); err == nil && len(batch) > 0 && len(batch[0].Embedding) > 0 {
		return batch[0].Embedding[0], nil
	}
	return nil, fmt.Errorf("embed/llamacpp: cannot parse embedding response")
}

func (l *LlamaCpp) endpoint() string {
	if l.Endpoint == "" {
		return "http://127.0.0.1:8080/embedding"
	}
	return l.Endpoint
}

func (l *LlamaCpp) healthURL() string {
	// /health sits on the server root, not under /embedding.
	ep := l.endpoint()
	if i := strings.LastIndex(ep, "/embedding"); i >= 0 {
		return ep[:i] + "/health"
	}
	return ep + "/health"
}

func (l *LlamaCpp) client() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// normaliseInPlace scales vec so its L2 norm is 1. Safe on zero-vectors.
func normaliseInPlace(vec []float32) {
	var sq float64
	for _, x := range vec {
		sq += float64(x) * float64(x)
	}
	if sq == 0 {
		return
	}
	n := float32(math.Sqrt(sq))
	for i := range vec {
		vec[i] /= n
	}
}
