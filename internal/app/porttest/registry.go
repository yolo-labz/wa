package porttest

// Registry is the canonical list of every port declared under
// internal/app. Feature 018 T1-18 exit gate: compile-time parity with
// the JSON-RPC contract registry in contracts/jsonrpc-v2.json.
//
// Adding a port means:
//  1. Declare the interface in internal/app/ports*.go.
//  2. Add the name here, in alphabetical order.
//  3. Provide a RunXxxContract runner in this package (or fold it into
//     RunContractSuite for the pre-017 intersection set).
//
// CLAUDE.md rule 20 (Cockburn: no fixed port count) permits the set to
// grow; every growth MUST touch this file in the same commit as the
// port declaration.
var Registry = []string{
	"Allowlist",
	"AuditLog",
	"ContactDirectory",
	"ContactSearcher",
	"DraftStore",
	"Embedder",
	"EventBuffer",
	"EventBus",
	"EventStream",
	"EventSubscription",
	"GroupManager",
	"HistoryStore",
	"IdempotencyStore",
	"LabelManager",
	"MediaStore",
	"MessageSearcher",
	"MessageSender",
	"Pairer",
	"PresenceSender",
	"ReplySender",
	"ScheduledStore",
	"SessionStore",
	"ThreadReader",
	"Transcriber",
	"VectorIndex",
}
