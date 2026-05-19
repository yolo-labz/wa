package main

import (
	"errors"
	"strings"
	"testing"
)

// TestAppendStaleHintFor asserts spec 110i FR-003: the stale-build
// footer is emitted ONLY for cobra "unknown flag"/"unknown command"
// errors, and contains both the build version + the upgrade hint.
func TestAppendStaleHintFor(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantHint bool
	}{
		{
			name:     "unknown flag",
			err:      errors.New("unknown flag: --remote"),
			wantHint: true,
		},
		{
			name:     "unknown command",
			err:      errors.New("unknown command \"history\" for \"wa\""),
			wantHint: true,
		},
		{
			name:     "network error passes through",
			err:      errors.New("dial unix /tmp/wa.sock: connect: connection refused"),
			wantHint: false,
		},
		{
			name:     "JSON-RPC error passes through",
			err:      errors.New("rpc error -32100: not allowlisted"),
			wantHint: false,
		},
		{
			name:     "nil err is no-op",
			err:      nil,
			wantHint: false,
		},
	}
	const ver = "v0.5.0"
	const hint = "brew upgrade yolo-labz/tap/wa"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendStaleHintFor(tt.err, ver, hint)
			if tt.wantHint {
				if got == "" {
					t.Fatalf("expected stale-build footer, got empty string")
				}
				if !strings.Contains(got, "Build v0.5.0") {
					t.Errorf("expected build version in footer, got %q", got)
				}
				if !strings.Contains(got, "your build may be stale") {
					t.Errorf("expected stale-build sentence, got %q", got)
				}
				if !strings.Contains(got, "Upgrade: brew upgrade yolo-labz/tap/wa") {
					t.Errorf("expected upgrade hint in footer, got %q", got)
				}
			} else if got != "" {
				t.Errorf("expected empty footer, got %q", got)
			}
		})
	}
}
