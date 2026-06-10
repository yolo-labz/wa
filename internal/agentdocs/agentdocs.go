// Package agentdocs embeds the agent-readable surface (feature 111 /
// roadmap Phase 0.2): llms.txt and the machine-readable error catalog.
// These are the canonical copies — the repo-root llms.txt and
// docs/errors.json are human-facing mirrors kept byte-identical by
// TestMirrorsInSync. The REST adapter serves both unauthenticated
// (GET /llms.txt, GET /v1/errors): they contain no secrets and exist
// precisely so unauthenticated agents can discover how to integrate.
package agentdocs

import _ "embed"

// LLMsTxt is the llms.txt body served at GET /llms.txt.
//
//go:embed llms.txt
var LLMsTxt []byte

// ErrorsJSON is the wa.errors/v1 catalog served at GET /v1/errors.
//
//go:embed errors.json
var ErrorsJSON []byte
