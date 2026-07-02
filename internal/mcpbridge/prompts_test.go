package mcpbridge

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// renderPrompt fetches a prompt and returns the rendered user-message text.
func renderPrompt(t *testing.T, cs *mcp.ClientSession, name string, args map[string]string) string {
	t.Helper()
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("GetPrompt(%s): %v", name, err)
	}
	return res.Messages[0].Content.(*mcp.TextContent).Text
}

// promptNames collects the names of all prompts registered on srv.
func promptNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	for p, err := range cs.Prompts(context.Background(), nil) {
		if err != nil {
			t.Fatalf("Prompts iterator: %v", err)
		}
		out[p.Name] = true
	}
	return out
}

// TestPromptRegistration_Toolsets pins which prompts appear under each
// toolset selection, mirroring the scope contract of the tools they reference.
func TestPromptRegistration_Toolsets(t *testing.T) {
	t.Parallel()

	t.Run("all toolsets: both prompts registered", func(t *testing.T) {
		t.Parallel()
		names := promptNames(t, session(t, &fakeCaller{}, Config{}))
		if !names["catch_me_up"] {
			t.Error("catch_me_up missing with all toolsets")
		}
		if !names["draft_reply"] {
			t.Error("draft_reply missing with all toolsets")
		}
	})

	t.Run("contacts only: catch_me_up present, draft_reply absent", func(t *testing.T) {
		t.Parallel()
		names := promptNames(t, session(t, &fakeCaller{}, Config{Toolsets: []string{ToolsetContacts}}))
		if !names["catch_me_up"] {
			t.Error("catch_me_up missing with contacts toolset")
		}
		if names["draft_reply"] {
			t.Error("draft_reply must not appear without messages toolset")
		}
	})

	t.Run("messages only: draft_reply present, catch_me_up absent", func(t *testing.T) {
		t.Parallel()
		names := promptNames(t, session(t, &fakeCaller{}, Config{Toolsets: []string{ToolsetMessages}}))
		if !names["draft_reply"] {
			t.Error("draft_reply missing with messages toolset")
		}
		if names["catch_me_up"] {
			t.Error("catch_me_up must not appear without contacts toolset")
		}
	})

	t.Run("read-only does not suppress prompts", func(t *testing.T) {
		t.Parallel()
		names := promptNames(t, session(t, &fakeCaller{}, Config{ReadOnly: true}))
		if !names["catch_me_up"] || !names["draft_reply"] {
			t.Error("--read-only must not suppress prompts (they are instructions, not mutations)")
		}
	})
}

// TestPromptRendering_CatchMeUp verifies argument handling and that the
// rendered prompt references the correct tools.
func TestPromptRendering_CatchMeUp(t *testing.T) {
	t.Parallel()
	cs := session(t, &fakeCaller{}, Config{})

	t.Run("default hours=24 embedded in prompt text", func(t *testing.T) {
		t.Parallel()
		res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "catch_me_up"})
		if err != nil {
			t.Fatalf("GetPrompt: %v", err)
		}
		if len(res.Messages) == 0 {
			t.Fatal("expected at least one prompt message")
		}
		text := res.Messages[0].Content.(*mcp.TextContent).Text
		if !strings.Contains(text, "24") {
			t.Errorf("default prompt should embed 24 hours, got: %q", text)
		}
	})

	t.Run("explicit hours=48 reflected in output", func(t *testing.T) {
		t.Parallel()
		text := renderPrompt(t, cs, "catch_me_up", map[string]string{"hours": "48"})
		if !strings.Contains(text, "48") {
			t.Errorf("hours=48 not reflected in prompt, got: %q", text)
		}
	})

	t.Run("non-numeric hours is rejected before returning a prompt", func(t *testing.T) {
		t.Parallel()
		_, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{
			Name:      "catch_me_up",
			Arguments: map[string]string{"hours": "banana"},
		})
		if err == nil {
			t.Fatal("expected error for non-numeric hours argument")
		}
	})

	t.Run("prompt guides agent to use wa_list_chats and wa_get_thread", func(t *testing.T) {
		t.Parallel()
		text := renderPrompt(t, cs, "catch_me_up", nil)
		if !strings.Contains(text, "wa_list_chats") {
			t.Error("catch_me_up must reference wa_list_chats (tool the agent should call)")
		}
		if !strings.Contains(text, "wa_get_thread") {
			t.Error("catch_me_up must reference wa_get_thread (tool the agent should call)")
		}
	})
}

// TestPromptRendering_DraftReply verifies required/optional argument
// handling and that the draft-gate polling pattern is in the prompt.
func TestPromptRendering_DraftReply(t *testing.T) {
	t.Parallel()
	cs := session(t, &fakeCaller{}, Config{})

	t.Run("required chat argument embedded in rendered text", func(t *testing.T) {
		t.Parallel()
		text := renderPrompt(t, cs, "draft_reply", map[string]string{"chat": "5511999999999"})
		if !strings.Contains(text, "5511999999999") {
			t.Errorf("chat JID not embedded in prompt, got: %q", text)
		}
	})

	t.Run("missing chat argument returns an error", func(t *testing.T) {
		t.Parallel()
		_, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "draft_reply"})
		if err == nil {
			t.Fatal("expected error when required chat argument is absent")
		}
	})

	t.Run("optional context argument woven into prompt", func(t *testing.T) {
		t.Parallel()
		text := renderPrompt(t, cs, "draft_reply", map[string]string{"chat": "5511999999999", "context": "keep it brief"})
		if !strings.Contains(text, "keep it brief") {
			t.Errorf("optional context not in prompt, got: %q", text)
		}
	})

	t.Run("prompt references draft-gate polling tools", func(t *testing.T) {
		t.Parallel()
		text := renderPrompt(t, cs, "draft_reply", map[string]string{"chat": "5511999999999"})
		if !strings.Contains(text, "wa_get_thread") {
			t.Error("draft_reply must reference wa_get_thread")
		}
		if !strings.Contains(text, "wa_send_message") {
			t.Error("draft_reply must reference wa_send_message")
		}
		if !strings.Contains(text, "wa_draft_review") {
			t.Error("draft_reply must reference wa_draft_review for polling (draft-gate pattern)")
		}
	})
}
