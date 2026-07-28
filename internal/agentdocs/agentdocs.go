// Package agentdocs embeds the agent-readable surface (feature 111 /
// roadmap Phase 0.2): llms.txt and the machine-readable error catalog.
// These are the canonical copies — the repo-root llms.txt and
// docs/errors.json are human-facing mirrors kept byte-identical by
// TestMirrorsInSync. The REST adapter serves both unauthenticated
// (GET /llms.txt, GET /v1/errors): they contain no secrets and exist
// precisely so unauthenticated agents can discover how to integrate.
package agentdocs

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// LLMsTxt is the llms.txt body served at GET /llms.txt.
//
//go:embed llms.txt
var LLMsTxt []byte

// ErrorsJSON is the wa.errors/v1 catalog served at GET /v1/errors.
//
//go:embed errors.json
var ErrorsJSON []byte

// OpenAPIJSON is the OpenAPI 3.1 transport contract served at
// GET /openapi.json.
//
//go:embed openapi.json
var OpenAPIJSON []byte

// OpenRPCJSON is the OpenRPC 1.3 method catalog served at
// GET /openrpc.json.
//
//go:embed openrpc.json
var OpenRPCJSON []byte

// MethodParam is one documented parameter of one JSON-RPC method.
type MethodParam struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// ParamsByMethod decodes the catalog into its documented parameter list
// per method name.
//
// The catalog is a contract between three things that are edited
// separately: the params struct each handler decodes, this file, and the
// param map the MCP bridge forwards. Nothing in the type system holds
// them together, and a parameter this file names but no handler accepts
// is not a documentation nit — it is an agent following the published
// contract and getting -32602. This accessor exists so each of those
// three can be tested against the catalog instead of against a private
// copy of this parsing code.
func ParamsByMethod() (map[string][]MethodParam, error) {
	var doc struct {
		Methods []struct {
			Name   string        `json:"name"`
			Params []MethodParam `json:"params"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(OpenRPCJSON, &doc); err != nil {
		return nil, fmt.Errorf("agentdocs: parse openrpc.json: %w", err)
	}
	out := make(map[string][]MethodParam, len(doc.Methods))
	for _, m := range doc.Methods {
		out[m.Name] = m.Params
	}
	return out, nil
}
