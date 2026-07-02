package evals

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// flagUpdate enables golden-file regeneration:
//
//	go test -run TestToolSnapshot ./internal/mcpbridge/evals/ -update
var flagUpdate = flag.Bool("update", false, "regenerate tools.snapshot.json")

// scenario is one scripted task that exercises the CONTRACT an LLM agent
// relies on when choosing and invoking a tool. Each scenario justifies the
// tool's presence in the 12-tool budget (spec 111 §Risks: "New tools need
// an eval-set scenario justifying them").
type scenario struct {
	name                 string   // human-readable task label
	taskDescription      string   // what an agent is trying to accomplish
	expectedTool         string   // the tool the agent must select
	requiredArgKeys      []string // keys the tool input schema must expose
	descriptionMustCover []string // case-insensitive substrings the description must contain
}

// scenarios is the 10-entry eval set required by spec 111 FR-111-08.
// All assertions run against the draft-mode, all-toolsets registry
// (mcpbridge.Config{}) — the default an operator gets with `wa mcp serve`.
var scenarios = []scenario{
	{
		name:            "send-text-message",
		taskDescription: "Send a text greeting to a contact's phone number",
		expectedTool:    "wa_send_message",
		requiredArgKeys: []string{"to", "body"},
		// Draft-gate polling pattern must be documented so agents know to
		// poll wa_draft_review, not assume immediate delivery (spec 111 §Risks).
		descriptionMustCover: []string{"draft", "wa_draft_review", "pending_review"},
	},
	{
		name:            "full-text-search",
		taskDescription: "Find all messages that mention a project name",
		expectedTool:    "wa_search_messages",
		requiredArgKeys: []string{"query"},
		// FR-005a channel-envelope contract: snippets are untrusted.
		descriptionMustCover: []string{"channel", "untrusted"},
	},
	{
		name:            "read-conversation-thread",
		taskDescription: "Read the recent messages of a specific chat",
		expectedTool:    "wa_get_thread",
		requiredArgKeys: []string{"chat"},
		// Cursor-pagination and channel-envelope both required in description.
		descriptionMustCover: []string{"channel", "cursor"},
	},
	{
		name:            "resolve-contact-by-name",
		taskDescription: "Look up the WhatsApp JID for someone named 'Alice'",
		expectedTool:    "wa_resolve_contact",
		requiredArgKeys: []string{"query"},
		// Contract: resolves names to JIDs (names-not-JIDs convention).
		descriptionMustCover: []string{"JID", "name"},
	},
	{
		name:            "schedule-future-send",
		taskDescription: "Schedule a reminder message for next Monday at 09:00",
		expectedTool:    "wa_schedule_message",
		requiredArgKeys: []string{"to", "body", "send_at"},
		// Scheduled sends route through the draft queue; agent must know
		// to poll wa_draft_review for status (same contract as send_message).
		descriptionMustCover: []string{"draft", "wa_draft_review"},
	},
	{
		name:            "send-media-file",
		taskDescription: "Share a PDF document with a contact over WhatsApp",
		expectedTool:    "wa_send_media",
		requiredArgKeys: []string{"to", "path"},
		// Draft-gate applies to media sends too; polling pattern required.
		descriptionMustCover: []string{"draft", "wa_draft_review"},
	},
	{
		name:            "transcribe-voice-note",
		taskDescription: "Get the text transcript of a voice message someone sent",
		expectedTool:    "wa_transcribe_voice",
		requiredArgKeys: []string{"message_id"},
		// Transcript is untrusted inbound content (SEC-02).
		descriptionMustCover: []string{"untrusted"},
	},
	{
		name:            "check-connection-status",
		taskDescription: "Verify the daemon is connected before attempting to send",
		expectedTool:    "wa_status",
		requiredArgKeys: []string{},
		// "Check this before assuming sends can succeed" is the key guidance.
		descriptionMustCover: []string{"before"},
	},
	{
		name:            "wait-for-reply-after-send",
		taskDescription: "Block and wait for the contact's reply after sending a message",
		expectedTool:    "wa_wait_for_reply",
		requiredArgKeys: []string{},
		// Must document the use-after-send pattern and the timeout escape.
		descriptionMustCover: []string{"timeout", "after sending"},
	},
	{
		name:            "inspect-pending-drafts",
		taskDescription: "Check which outbound messages are waiting for human approval",
		expectedTool:    "wa_draft_review",
		requiredArgKeys: []string{},
		// Read-only contract: only humans can approve/reject (draft-gate invariant).
		descriptionMustCover: []string{"read-only", "human"},
	},
}

// serverTools builds a full-toolset draft-mode MCP bridge, connects an
// in-process eval client, lists all tools, and returns them by name.
// A context-cancel shuts down the server goroutine on test cleanup.
func serverTools(tb testing.TB) map[string]*mcp.Tool {
	tb.Helper()
	noop := mcpbridge.Caller(func(_ context.Context, _ string, _ any) (json.RawMessage, *mcpbridge.RPCError, error) {
		return json.RawMessage(`{}`), nil, nil
	})
	bridge, err := mcpbridge.NewServer(noop, mcpbridge.Config{})
	if err != nil {
		tb.Fatalf("mcpbridge.NewServer: %v", err)
	}
	srvT, cliT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	go func() { _ = bridge.Run(ctx, srvT) }()
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "eval", Version: "0"}, nil).Connect(context.Background(), cliT, nil)
	if err != nil {
		tb.Fatalf("eval client connect: %v", err)
	}
	tb.Cleanup(func() { _ = sess.Close() })
	listed, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		tb.Fatalf("sess.ListTools: %v", err)
	}
	catalog := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, t := range listed.Tools {
		catalog[t.Name] = t
	}
	return catalog
}

// schemaArgKeys returns the sorted property names from an MCP tool's
// InputSchema, which the client receives as a map[string]any after the
// JSON round-trip through the MCP protocol.
func schemaArgKeys(schema any) []string {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	props, _ := m["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// descCovers reports whether desc contains sub (case-insensitive).
func descCovers(desc, sub string) bool {
	return strings.Contains(strings.ToLower(desc), strings.ToLower(sub))
}

// TestEvalSet is the CI-resident agentic eval harness required by
// spec 111 FR-111-08 (spec.md lines 158–160). It runs 10 scripted tasks
// against the live MCP tool registry and verifies four properties:
//
//	(a) The expected tool exists for every scenario.
//	(b) The tool's input schema exposes every required argument key.
//	(c) The tool description covers every contract item the LLM must read.
//	(d) Global invariants: budget ≤ 12 tools, no empty description or nil
//	    schema, every send-capable tool documents the draft/review pattern.
func TestEvalSet(t *testing.T) {
	t.Parallel()
	tools := serverTools(t)

	// (a)+(b)+(c) per-scenario checks.
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			tool, ok := tools[sc.expectedTool]
			if !ok {
				t.Fatalf("tool %q not registered (required by scenario %q)", sc.expectedTool, sc.name)
			}
			argSet := make(map[string]bool)
			for _, k := range schemaArgKeys(tool.InputSchema) {
				argSet[k] = true
			}
			for _, k := range sc.requiredArgKeys {
				if !argSet[k] {
					t.Errorf("tool %q schema missing required arg %q (scenario %q)", sc.expectedTool, k, sc.name)
				}
			}
			for _, item := range sc.descriptionMustCover {
				if !descCovers(tool.Description, item) {
					t.Errorf("tool %q description does not cover %q\n  description: %q", sc.expectedTool, item, tool.Description)
				}
			}
		})
	}

	// (d) Global invariants.
	t.Run("global:tool-count-budget", func(t *testing.T) {
		t.Parallel()
		if n := len(tools); n > 12 {
			t.Errorf("tool count %d exceeds 12-tool budget (spec 111 §Risks)", n)
		}
	})

	t.Run("global:non-empty-description", func(t *testing.T) {
		t.Parallel()
		for name, tool := range tools {
			if strings.TrimSpace(tool.Description) == "" {
				t.Errorf("tool %q has an empty description", name)
			}
		}
	})

	t.Run("global:schema-not-nil", func(t *testing.T) {
		t.Parallel()
		for name, tool := range tools {
			if tool.InputSchema == nil {
				t.Errorf("tool %q has nil InputSchema", name)
			}
		}
	})

	t.Run("global:send-tools-document-draft-contract", func(t *testing.T) {
		t.Parallel()
		// All registered send-capable tools must document the draft/review
		// pattern so an agent knows to poll wa_draft_review and not assume
		// delivery. Tools absent when send-mode=deny are skipped.
		for _, name := range []string{"wa_send_message", "wa_send_media", "wa_schedule_message"} {
			tool, ok := tools[name]
			if !ok {
				continue
			}
			if !descCovers(tool.Description, "draft") {
				t.Errorf("send tool %q description must document the draft contract, got: %q", name, tool.Description)
			}
		}
	})
}

// toolEntry is the golden snapshot record: name + sorted arg keys.
// Descriptions are excluded so tuning them never breaks the snapshot;
// only schema surface changes (new/renamed tools or args) need review.
type toolEntry struct {
	Name    string   `json:"name"`
	ArgKeys []string `json:"arg_keys"`
}

const snapshotPath = "tools.snapshot.json"

// TestToolSnapshot compares the live tool registry against the committed
// tools.snapshot.json golden file. Drift fails CI, forcing deliberate
// review of any schema surface change. Regenerate with:
//
//	go test -run TestToolSnapshot ./internal/mcpbridge/evals/ -update
func TestToolSnapshot(t *testing.T) {
	t.Parallel()
	catalog := serverTools(t)

	// Build a stable sorted slice of entries.
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]toolEntry, len(names))
	for i, name := range names {
		keys := schemaArgKeys(catalog[name].InputSchema)
		if keys == nil {
			keys = []string{}
		}
		entries[i] = toolEntry{Name: name, ArgKeys: keys}
	}

	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	raw = append(raw, '\n')

	if *flagUpdate {
		if err := os.WriteFile(snapshotPath, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", snapshotPath, err)
		}
		t.Logf("wrote %s", snapshotPath)
		return
	}

	golden, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read %s (run with -update to generate): %v", snapshotPath, err)
	}
	if string(raw) != string(golden) {
		t.Errorf("tool snapshot drift — regenerate:\n  go test -run TestToolSnapshot ./internal/mcpbridge/evals/ -update\nwant:\n%sgot:\n%s", golden, raw)
	}
}
