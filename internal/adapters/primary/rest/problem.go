package rest

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// problemDetails is the RFC 9457 application/problem+json shape every
// non-200 REST response carries. The rule agents can rely on: an HTTP
// error status means problem+json; HTTP 200 on /v1/rpc means a
// JSON-RPC envelope (whose error member covers dispatcher failures —
// JSON-RPC errors are application-level results, not transport
// failures, so they keep their 200 contract).
//
// `code` is an RFC 9457 §3.2 extension member carrying the JSON-RPC
// error code so existing integer-code clients keep a stable field
// across both shapes.
type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   int    `json:"code"`
}

// problemCatalog maps JSON-RPC error codes to their catalog identity
// (name + canonical message) parsed from the embedded machine-readable
// catalog — the same bytes served at /v1/errors, so the `type` URI a
// problem carries always dereferences to its own catalog row.
var problemCatalog = sync.OnceValue(func() map[int]struct{ Name, Message string } {
	var cat struct {
		Errors []struct {
			Code    int    `json:"code"`
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	out := make(map[int]struct{ Name, Message string })
	if err := json.Unmarshal(agentdocs.ErrorsJSON, &cat); err != nil {
		return out // empty map → generic fallbacks below
	}
	for _, e := range cat.Errors {
		out[e.Code] = struct{ Name, Message string }{e.Name, e.Message}
	}
	return out
})

// newProblem builds the RFC 9457 body for a JSON-RPC error code. The
// type URI is an absolute-path reference into the served error
// catalog (RFC 9457 §3.1.1 allows relative URI references); agents
// resolve it against the request host and land on the catalog row
// with the remediation text.
func newProblem(status, code int, detail string) problemDetails {
	title := "error"
	typeURI := "/v1/errors"
	if entry, ok := problemCatalog()[code]; ok {
		title = entry.Message
		typeURI = "/v1/errors#" + entry.Name
	}
	return problemDetails{
		Type:   typeURI,
		Title:  title,
		Status: status,
		Detail: detail,
		Code:   code,
	}
}

// writeProblem emits an RFC 9457 response. Overrides any Content-Type
// a handler set earlier (handlers default to application/json for
// their success path).
func (s *Server) writeProblem(w http.ResponseWriter, status, code int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(newProblem(status, code, detail)); err != nil {
		s.log.Error("rest: encode problem response", "err", err)
	}
}
