package whatsmeow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// interactiveRender is the wire shape the socket adapter serialises for
// FR-130 interactive message events. Declared locally to the golden test
// so drift in the wire schema surfaces as a golden diff.
type interactiveRender struct {
	Kind        string              `json:"kind"`
	Interactive *interactivePayload `json:"interactive,omitempty"`
}

type interactivePayload struct {
	Subtype string              `json:"subtype"`
	Prompt  string              `json:"prompt"`
	Options []interactiveOption `json:"options"`
}

type interactiveOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func render(msg *waE2E.Message) ([]byte, error) {
	p := extractInteractive(msg)
	if p == nil {
		return []byte(`{"kind":"message"}`), nil
	}
	ir := interactiveRender{
		Kind: "message",
		Interactive: &interactivePayload{
			Subtype: p.Subtype.String(),
			Prompt:  p.Prompt,
			Options: make([]interactiveOption, 0, len(p.Options)),
		},
	}
	for _, o := range p.Options {
		ir.Interactive.Options = append(ir.Interactive.Options, interactiveOption{ID: o.ID, Label: o.Label})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ir); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TestInteractiveMessageRender is the T2-03 golden render covering both
// inbound list and button replies.
func TestInteractiveMessageRender(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		msg    *waE2E.Message
		golden string
	}{
		{
			name: "list",
			msg: &waE2E.Message{
				ListResponseMessage: &waE2E.ListResponseMessage{
					Title: new("Pick a plan"),
					SingleSelectReply: &waE2E.ListResponseMessage_SingleSelectReply{
						SelectedRowID: new("plan-basic"),
					},
				},
			},
			golden: "interactive_list.golden.json",
		},
		{
			name: "buttons",
			msg: &waE2E.Message{
				ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
					SelectedButtonID: new("btn-yes"),
					Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{
						SelectedDisplayText: "Yes",
					},
				},
			},
			golden: "interactive_buttons.golden.json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := render(tc.msg)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", tc.golden))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
				t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}

func TestInteractiveNilMessage(t *testing.T) {
	t.Parallel()
	if got := extractInteractive(nil); got != nil {
		t.Errorf("nil message should yield nil payload, got %+v", got)
	}
	if got := extractInteractive(&waE2E.Message{}); got != nil {
		t.Errorf("empty message should yield nil payload, got %+v", got)
	}
}

// nativeFlowMsg builds an InteractiveResponseMessage with a native-flow
// response carrying the given name, paramsJSON and body text (issue #201).
func nativeFlowMsg(name, paramsJSON, body string) *waE2E.Message {
	return &waE2E.Message{
		InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{
			// NativeFlowResponseMessage lives in a protobuf oneof, so it is
			// set via its generated wrapper option type.
			InteractiveResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage_{
				NativeFlowResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage{
					Name:       new(name),
					ParamsJSON: new(paramsJSON),
					Version:    proto.Int32(3),
				},
			},
			Body: &waE2E.InteractiveResponseMessage_Body{
				Text: new(body),
			},
		},
	}
}

// TestInteractive_NativeFlowReply is the issue #201 decode coverage for the
// 4th (native-flow) reply type that Cloud-API IVR menus use.
func TestInteractive_NativeFlowReply(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		msg         *waE2E.Message
		wantNil     bool
		wantSubtype domain.InteractiveSubtype
		wantPrompt  string
		wantOptID   string
		wantOptLbl  string
		wantNoOpts  bool
	}{
		{
			// Canonical single-select IVR pick: id + human description, list-typed.
			name:        "single_select id+description",
			msg:         nativeFlowMsg("single_select", `{"id":"unit_recife_riomar","description":"Unidade Recife - Rio Mar"}`, "Choose a unit"),
			wantSubtype: domain.InteractiveList,
			wantPrompt:  "Choose a unit",
			wantOptID:   "unit_recife_riomar",
			wantOptLbl:  "Unidade Recife - Rio Mar",
		},
		{
			// Quick-reply button tap: id only, defaults to Buttons subtype,
			// label falls back to the body text.
			name:        "quick_reply id only falls back to body label",
			msg:         nativeFlowMsg("quick_reply", `{"id":"btn_agendar"}`, "Agendar atendimento"),
			wantSubtype: domain.InteractiveButtons,
			wantPrompt:  "Agendar atendimento",
			wantOptID:   "btn_agendar",
			wantOptLbl:  "Agendar atendimento",
		},
		{
			// Unknown Name → Buttons default; `title` is accepted as label.
			name:        "unknown name uses title as label and buttons default",
			msg:         nativeFlowMsg("", `{"id":"opt_7","title":"Option Seven"}`, ""),
			wantSubtype: domain.InteractiveButtons,
			wantPrompt:  "",
			wantOptID:   "opt_7",
			wantOptLbl:  "Option Seven",
		},
		{
			// Malformed JSON must NOT panic; the body prompt is still
			// preserved, with no option materialised.
			name:        "malformed params keeps prompt, no option",
			msg:         nativeFlowMsg("single_select", `{"id":`, "Pick one"),
			wantSubtype: domain.InteractiveList,
			wantPrompt:  "Pick one",
			wantNoOpts:  true,
		},
		{
			// Multi-field flow FORM answers (no id key) → prompt-only payload.
			name:        "flow form answers without id → prompt only",
			msg:         nativeFlowMsg("flow", `{"overall_experience":"0_very_satisfied","flow_token":"unused"}`, "Survey complete"),
			wantSubtype: domain.InteractiveButtons,
			wantPrompt:  "Survey complete",
			wantNoOpts:  true,
		},
		{
			// Bare-array params (non-object JSON) is malformed for our
			// purposes; no panic, prompt preserved.
			name:        "bare array params, no panic",
			msg:         nativeFlowMsg("single_select", `["a","b"]`, "Header"),
			wantSubtype: domain.InteractiveList,
			wantPrompt:  "Header",
			wantNoOpts:  true,
		},
		{
			// Empty params AND empty body → nothing to surface → nil.
			name:    "empty params and empty body → nil",
			msg:     nativeFlowMsg("single_select", "", ""),
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractInteractive(tc.msg)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("want nil payload, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("extractInteractive returned nil, want payload")
			}
			if got.Subtype != tc.wantSubtype {
				t.Errorf("subtype = %v, want %v", got.Subtype, tc.wantSubtype)
			}
			if got.Prompt != tc.wantPrompt {
				t.Errorf("prompt = %q, want %q", got.Prompt, tc.wantPrompt)
			}
			if tc.wantNoOpts {
				if len(got.Options) != 0 {
					t.Errorf("options = %d, want 0 (%+v)", len(got.Options), got.Options)
				}
				return
			}
			if len(got.Options) != 1 {
				t.Fatalf("options = %d, want 1 (%+v)", len(got.Options), got.Options)
			}
			if got.Options[0].ID != tc.wantOptID {
				t.Errorf("option.ID = %q, want %q", got.Options[0].ID, tc.wantOptID)
			}
			if got.Options[0].Label != tc.wantOptLbl {
				t.Errorf("option.Label = %q, want %q", got.Options[0].Label, tc.wantOptLbl)
			}
		})
	}
}

// TestInteractive_NativeFlowNilSafety confirms the defensive nil paths in
// the native-flow branch never panic (issue #201).
func TestInteractive_NativeFlowNilSafety(t *testing.T) {
	t.Parallel()
	// InteractiveResponseMessage present but NativeFlowResponseMessage nil.
	msg := &waE2E.Message{InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{}}
	if got := extractInteractive(msg); got != nil {
		t.Errorf("interactive response without native-flow should yield nil, got %+v", got)
	}
}
