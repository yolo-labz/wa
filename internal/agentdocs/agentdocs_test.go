package agentdocs

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// catalog is the parsed shape of errors.json.
type catalog struct {
	Schema string `json:"schema"`
	Errors []struct {
		Code        int    `json:"code"`
		Name        string `json:"name"`
		Message     string `json:"message"`
		Retryable   bool   `json:"retryable"`
		Remediation string `json:"remediation"`
	} `json:"errors"`
}

func parsedCatalog(t *testing.T) catalog {
	t.Helper()
	var c catalog
	if err := json.Unmarshal(ErrorsJSON, &c); err != nil {
		t.Fatalf("errors.json invalid: %v", err)
	}
	if c.Schema != "wa.errors/v1" {
		t.Fatalf("schema = %q, want wa.errors/v1", c.Schema)
	}
	return c
}

// TestErrorsJSONShape pins basic integrity: valid JSON, unique codes,
// every row has name + remediation (the remediation IS the product).
func TestErrorsJSONShape(t *testing.T) {
	t.Parallel()
	c := parsedCatalog(t)
	seen := map[int]bool{}
	for _, e := range c.Errors {
		if seen[e.Code] {
			t.Errorf("duplicate code %d", e.Code)
		}
		seen[e.Code] = true
		if e.Name == "" || e.Remediation == "" {
			t.Errorf("code %d missing name/remediation", e.Code)
		}
	}
}

// TestAppCatalogCovered is the drift guard: every code the app layer
// can emit (registered via newRPCErr) must appear in errors.json. A
// failure means a new typed error shipped without documenting it —
// add a row to internal/agentdocs/errors.json (and mirrors).
func TestAppCatalogCovered(t *testing.T) {
	t.Parallel()
	c := parsedCatalog(t)
	documented := map[int]bool{}
	for _, e := range c.Errors {
		documented[e.Code] = true
	}
	for _, entry := range app.RPCErrorCatalog() {
		if !documented[entry.Code] {
			t.Errorf("app error %d (%s) missing from errors.json", entry.Code, entry.Message)
		}
	}
}

// TestMirrorsInSync keeps the human-facing copies byte-identical to
// the embedded canon: repo-root llms.txt and docs/errors.json.
func TestMirrorsInSync(t *testing.T) {
	t.Parallel()
	for mirror, canon := range map[string][]byte{
		"../../llms.txt":         LLMsTxt,
		"../../docs/errors.json": ErrorsJSON,
	} {
		got, err := os.ReadFile(mirror)
		if err != nil {
			t.Errorf("mirror %s unreadable: %v", mirror, err)
			continue
		}
		if !bytes.Equal(got, canon) {
			t.Errorf("%s out of sync with internal/agentdocs — copy the canonical file over it", mirror)
		}
	}
}
