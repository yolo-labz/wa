package app

import "testing"

func TestEagerOnlyForAutoApproval(t *testing.T) {
	cases := []struct {
		name     string
		mode     ApprovalMode
		override bool
		want     bool
	}{
		{"auto-no-override", ApprovalAuto, false, true},
		{"manual-no-override", ApprovalManual, false, false},
		{"draft-no-override", ApprovalDraft, false, false},
		{"auto-with-override", ApprovalAuto, true, true},
		{"manual-with-override", ApprovalManual, true, true},
		{"draft-with-override", ApprovalDraft, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TranscribePolicy(c.mode, c.override)
			if got != c.want {
				t.Fatalf("TranscribePolicy(%v, %v): got %v want %v", c.mode, c.override, got, c.want)
			}
		})
	}
}

func TestLazyFetchTranscript(t *testing.T) {
	// The happy-path inverse: for the default manual approval mode, no
	// override, nothing should be transcribed eagerly.
	if TranscribePolicy(ApprovalManual, false) {
		t.Fatalf("ApprovalManual + override=false: want lazy (false), got eager (true)")
	}
}
