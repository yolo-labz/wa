package whatsmeow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
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
					Title: proto.String("Pick a plan"),
					SingleSelectReply: &waE2E.ListResponseMessage_SingleSelectReply{
						SelectedRowID: proto.String("plan-basic"),
					},
				},
			},
			golden: "interactive_list.golden.json",
		},
		{
			name: "buttons",
			msg: &waE2E.Message{
				ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
					SelectedButtonID: proto.String("btn-yes"),
					Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{
						SelectedDisplayText: "Yes",
					},
				},
			},
			golden: "interactive_buttons.golden.json",
		},
	}

	for _, tc := range cases {
		tc := tc
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
