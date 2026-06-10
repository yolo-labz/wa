package socket

import (
	"encoding/json"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// TestSocketCodesDocumented is the socket-side drift guard for the
// agent-readable error catalog: every code in errCodeName must appear
// in internal/agentdocs/errors.json (feature 111 / roadmap 0.2).
func TestSocketCodesDocumented(t *testing.T) {
	t.Parallel()
	var c struct {
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(agentdocs.ErrorsJSON, &c); err != nil {
		t.Fatalf("errors.json invalid: %v", err)
	}
	documented := map[int]bool{}
	for _, e := range c.Errors {
		documented[e.Code] = true
	}
	for code, name := range errCodeName {
		if !documented[int(code)] {
			t.Errorf("socket code %d (%s) missing from internal/agentdocs/errors.json", code, name)
		}
	}
}
