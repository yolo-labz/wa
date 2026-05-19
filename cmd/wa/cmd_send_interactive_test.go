package main

import (
	"testing"
)

// resetSendFlags wipes every send-command global between subtests so each
// scenario starts from a known-empty state.
func resetSendFlags() {
	sendTo = ""
	sendBody = ""
	sendIdempotencyKey = ""
	sendListRowID = ""
	sendListRowTitle = ""
	sendButtonID = ""
	sendTemplateButton = ""
	sendButtonDisplay = ""
}

// TestPickedSendModes_Mutex — spec 110j FR-005: exactly one of --body,
// --list-row-id, --button-id, --template-button-id may be set. Two or more
// is a usage error (exit 64); zero is a usage error too.
func TestPickedSendModes_Mutex(t *testing.T) {
	cases := []struct {
		name   string
		setup  func()
		wantN  int
		expect []string
	}{
		{
			name:   "none",
			setup:  func() {},
			wantN:  0,
			expect: nil,
		},
		{
			name:   "body only",
			setup:  func() { sendBody = "hi" },
			wantN:  1,
			expect: []string{"--body"},
		},
		{
			name:   "list only",
			setup:  func() { sendListRowID = "row-7" },
			wantN:  1,
			expect: []string{"--list-row-id"},
		},
		{
			name:   "button only",
			setup:  func() { sendButtonID = "btn-1" },
			wantN:  1,
			expect: []string{"--button-id"},
		},
		{
			name:   "template only",
			setup:  func() { sendTemplateButton = "tpl-1" },
			wantN:  1,
			expect: []string{"--template-button-id"},
		},
		{
			name: "body + list",
			setup: func() {
				sendBody = "hi"
				sendListRowID = "row-7"
			},
			wantN:  2,
			expect: []string{"--body", "--list-row-id"},
		},
		{
			name: "list + button",
			setup: func() {
				sendListRowID = "row-7"
				sendButtonID = "btn-1"
			},
			wantN:  2,
			expect: []string{"--list-row-id", "--button-id"},
		},
		{
			name: "button + template",
			setup: func() {
				sendButtonID = "btn-1"
				sendTemplateButton = "tpl-1"
			},
			wantN:  2,
			expect: []string{"--button-id", "--template-button-id"},
		},
		{
			name: "all four",
			setup: func() {
				sendBody = "hi"
				sendListRowID = "row-7"
				sendButtonID = "btn-1"
				sendTemplateButton = "tpl-1"
			},
			wantN:  4,
			expect: []string{"--body", "--list-row-id", "--button-id", "--template-button-id"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSendFlags()
			tc.setup()
			got := pickedSendModes()
			if len(got) != tc.wantN {
				t.Fatalf("len = %d, want %d (got %v)", len(got), tc.wantN, got)
			}
			for i := range got {
				if got[i] != tc.expect[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.expect[i])
				}
			}
		})
		resetSendFlags()
	}
}

// TestBuildSendParams_Routing — spec 110j FR-005: each picked mode routes
// to its own JSON-RPC method with the right param keys.
func TestBuildSendParams_Routing(t *testing.T) {
	cases := []struct {
		name       string
		setup      func()
		wantMethod string
		wantKeys   []string
	}{
		{
			name: "body → send",
			setup: func() {
				sendTo = "5511999999999@s.whatsapp.net"
				sendBody = "hi"
			},
			wantMethod: "send",
			wantKeys:   []string{"to", "body"},
		},
		{
			name: "list-row-id → send.listResponse",
			setup: func() {
				sendTo = "5511999999999@s.whatsapp.net"
				sendListRowID = "row-7"
			},
			wantMethod: "send.listResponse",
			wantKeys:   []string{"to", "rowId"},
		},
		{
			name: "list-row-id + title → send.listResponse with title",
			setup: func() {
				sendTo = "5511999999999@s.whatsapp.net"
				sendListRowID = "row-7"
				sendListRowTitle = "Atendente"
			},
			wantMethod: "send.listResponse",
			wantKeys:   []string{"to", "rowId", "title"},
		},
		{
			name: "button-id → send.buttonResponse kind=button",
			setup: func() {
				sendTo = "5511999999999@s.whatsapp.net"
				sendButtonID = "btn-1"
			},
			wantMethod: "send.buttonResponse",
			wantKeys:   []string{"to", "buttonId", "kind"},
		},
		{
			name: "template-button-id → send.buttonResponse kind=templateButton",
			setup: func() {
				sendTo = "5511999999999@s.whatsapp.net"
				sendTemplateButton = "tpl-7"
			},
			wantMethod: "send.buttonResponse",
			wantKeys:   []string{"to", "buttonId", "kind"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSendFlags()
			tc.setup()
			method, params, err := buildSendParams()
			if err != nil {
				t.Fatalf("buildSendParams: %v", err)
			}
			if method != tc.wantMethod {
				t.Errorf("method = %q, want %q", method, tc.wantMethod)
			}
			for _, k := range tc.wantKeys {
				if _, ok := params[k]; !ok {
					t.Errorf("missing param key %q (got %v)", k, params)
				}
			}
			// Mode-specific shape checks.
			if tc.wantMethod == "send.buttonResponse" {
				wantKind := "button"
				if sendTemplateButton != "" {
					wantKind = "templateButton"
				}
				if got := params["kind"]; got != wantKind {
					t.Errorf("kind = %v, want %q", got, wantKind)
				}
			}
		})
		resetSendFlags()
	}
}
