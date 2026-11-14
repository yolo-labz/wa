package agentdocs

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
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

// TestOpenRPC_SyncForceDocumentsAllowlist (WA-S4C2-FIX-FOLLOW, p8 I4): an
// agent reading the catalog learns that sync.force needs the read allowlist.
func TestOpenRPC_SyncForceDocumentsAllowlist(t *testing.T) {
	t.Parallel()
	var doc struct {
		Methods []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(OpenRPCJSON, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range doc.Methods {
		if m.Name == "sync.force" {
			for _, want := range []string{"allowlist", "read", "not_allowlisted"} {
				if !strings.Contains(m.Summary, want) {
					t.Errorf("sync.force summary lacks %q: %s", want, m.Summary)
				}
			}
			return
		}
	}
	t.Fatal("sync.force missing from openrpc.json")
}

// TestOpenRPC_WaitDocumentsCap (p8 S4c1 I2): the socket/REST `wait` blocks at
// most app.MaxWaitMs, defaults to it, and clamps a longer timeoutMs without
// an error; clients that relied on the old 30 s default must re-poll.
func TestOpenRPC_WaitDocumentsCap(t *testing.T) {
	t.Parallel()
	var doc struct {
		Methods []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
			Params  []struct {
				Name   string `json:"name"`
				Schema struct {
					Description string `json:"description"`
				} `json:"schema"`
			} `json:"params"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(OpenRPCJSON, &doc); err != nil {
		t.Fatal(err)
	}
	limit := strconv.Itoa(app.MaxWaitMs)
	for _, m := range doc.Methods {
		if m.Name != "wait" {
			continue
		}
		for _, p := range m.Params {
			if p.Name != "timeoutMs" {
				continue
			}
			d := p.Schema.Description
			for _, want := range []string{limit, "default", "clamp", "-32003", "timed_out"} {
				if !strings.Contains(d, want) {
					t.Errorf("wait.timeoutMs description %q lacks %q", d, want)
				}
			}
			return
		}
	}
	t.Fatal("openrpc.json has no wait.timeoutMs param")
}

// TestOpenRPC_SendDocumentsReturnDeadline: send and sendMedia tell a caller
// that a send past 60 s answers a job envelope to poll, never to resend.
func TestOpenRPC_SendDocumentsReturnDeadline(t *testing.T) {
	t.Parallel()
	var doc struct {
		Methods []struct{ Name, Summary string } `json:"methods"`
	}
	if err := json.Unmarshal(OpenRPCJSON, &doc); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, m := range doc.Methods {
		if m.Name == "send" || m.Name == "sendMedia" {
			seen++
			for _, want := range []string{"60 s", "timed_out", "jobs.get", "never resend"} {
				if !strings.Contains(m.Summary, want) {
					t.Errorf("%s summary lacks %q: %s", m.Name, want, m.Summary)
				}
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d of send, sendMedia", seen)
	}
}
