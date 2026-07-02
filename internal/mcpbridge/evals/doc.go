// Package evals is the agentic eval harness for spec 111 FR-111-08
// (specs/111-mcp-primary-adapter/spec.md lines 158–160). The spec places
// this under internal/app/mcptools/evals/; this package lives at
// internal/mcpbridge/evals/ because the 12 wa_* tools under evaluation are
// registered in internal/mcpbridge/tools.go and the test harness reuses
// that package's exported NewServer/Config/Caller surface directly.
package evals
