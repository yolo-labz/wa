package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// ErrCloudOptOut signals the cloud embedder is disabled by user policy.
// Agents MUST NOT auto-enable cloud embedders — opt-in is explicit via
// voyage.env (FR-102) presence. T3-04.
var ErrCloudOptOut = errors.New("embed/voyage: cloud embedder not opted-in")

// Voyage is the cloud-HTTP embedder backed by voyageai.com. Model is
// voyage-3 (1024-dim) by default. Credentials are read from voyage.env
// in the profile config dir so they never leak into a shared config.toml.
type Voyage struct {
	// APIKey authenticates requests. Usually loaded via LoadVoyageAPIKey.
	APIKey string
	// Endpoint defaults to https://api.voyageai.com/v1/embeddings.
	Endpoint string
	// Model is the embedding model tag. Defaults to "voyage-3".
	Model string
	// Dim is the advertised output dimensionality; Voyage's voyage-3 is 1024.
	Dim int
	// HTTP is the request transport; nil means http.Client{Timeout:30s}.
	HTTP *http.Client
}

// NewVoyage returns a Voyage adapter with defaults matching voyage-3.
// APIKey stays empty — callers MUST inject it via LoadVoyageAPIKey.
func NewVoyage() *Voyage {
	return &Voyage{
		Endpoint: "https://api.voyageai.com/v1/embeddings",
		Model:    "voyage-3",
		Dim:      1024,
	}
}

// LoadVoyageAPIKey reads VOYAGE_API_KEY from the profile's voyage.env
// file. Returns ErrCloudOptOut when the file is absent — the feature
// flag alone is not enough; the operator must materialise the key
// deliberately. FR-102.
//
// Expected file layout (single-line, key=value, trailing newline OK):
//
//	VOYAGE_API_KEY=pa-live-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
func LoadVoyageAPIKey(configDir string) (string, error) {
	path := filepath.Join(configDir, "voyage.env")
	// #nosec G304 -- configDir is the operator-configured profile dir
	// ($XDG_CONFIG_HOME/wa/<profile>); adapter reads its own secret.
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrCloudOptOut
	}
	if err != nil {
		return "", fmt.Errorf("embed/voyage: read voyage.env: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == "VOYAGE_API_KEY" {
			return strings.Trim(strings.TrimSpace(v), `"'`), nil
		}
	}
	return "", fmt.Errorf("embed/voyage: VOYAGE_API_KEY not set in %s", path)
}

// Info returns the adapter's identity.
func (v *Voyage) Info() app.EmbedderInfo {
	return app.EmbedderInfo{Model: v.Model, Dim: v.Dim}
}

// Embed posts a single input to Voyage's /v1/embeddings and returns
// the first embedding. Adapter refuses to make the call when APIKey
// is empty so agents cannot accidentally enable the cloud path.
func (v *Voyage) Embed(ctx context.Context, text string) (domain.Embedding, error) {
	if v.APIKey == "" {
		return domain.Embedding{}, ErrCloudOptOut
	}
	if strings.TrimSpace(text) == "" {
		return domain.Embedding{}, errors.New("embed/voyage: empty text")
	}
	payload := map[string]any{
		"model":      v.Model,
		"input":      []string{text},
		"input_type": "document",
		"truncation": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/voyage: marshal: %w", err)
	}
	endpoint := v.Endpoint
	if endpoint == "" {
		endpoint = "https://api.voyageai.com/v1/embeddings"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/voyage: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.APIKey)
	resp, err := v.client().Do(req)
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/voyage: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return domain.Embedding{}, fmt.Errorf("embed/voyage: http %d: %s", resp.StatusCode, string(raw))
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return domain.Embedding{}, fmt.Errorf("embed/voyage: decode: %w", err)
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return domain.Embedding{}, errors.New("embed/voyage: empty embedding")
	}
	vec := decoded.Data[0].Embedding
	if len(vec) != v.Dim {
		return domain.Embedding{}, fmt.Errorf("embed/voyage: got dim %d want %d", len(vec), v.Dim)
	}
	normaliseInPlace(vec)
	return domain.Embedding{Model: v.Model, Dim: v.Dim, Vec: vec}, nil
}

func (v *Voyage) client() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}
