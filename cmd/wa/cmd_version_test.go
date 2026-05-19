package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPrintVersion_Human asserts that the human format matches the
// existing `wa version` output verbatim — spec 110i FR-001 requires
// `wa --version` print the SAME string as `wa version`.
func TestPrintVersion_Human(t *testing.T) {
	saved := version
	defer func() { version = saved }()
	version = "v1.2.3"

	var buf bytes.Buffer
	printVersion(&buf, false)

	want := "wa version v1.2.3\n"
	if got := buf.String(); got != want {
		t.Errorf("printVersion human: got %q, want %q", got, want)
	}
}

// TestPrintVersion_JSON asserts the schema string is preserved at
// `wa.version/v1` (FR-004 — no schema bump).
func TestPrintVersion_JSON(t *testing.T) {
	saved := version
	defer func() { version = saved }()
	version = "v1.2.3"

	var buf bytes.Buffer
	printVersion(&buf, true)

	got := buf.String()
	if !strings.Contains(got, `"schema":"wa.version/v1"`) {
		t.Errorf("expected schema wa.version/v1, got %q", got)
	}
	if !strings.Contains(got, `"version":"v1.2.3"`) {
		t.Errorf("expected version field v1.2.3, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected trailing newline for NDJSON, got %q", got)
	}
}

// TestArgvHasFlag covers the POSIX-ish scanner that intercepts
// --version before cobra subcommand routing.
func TestArgvHasFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
		want bool
	}{
		{"bare flag", []string{"--version"}, "version", true},
		{"bare flag on subcommand", []string{"send", "--version"}, "version", true},
		{"flag with =true", []string{"--version=true"}, "version", true},
		{"flag with =1", []string{"--version=1"}, "version", true},
		{"flag with =false", []string{"--version=false"}, "version", false},
		{"flag with =0", []string{"--version=0"}, "version", false},
		{"absent", []string{"send", "--to", "foo"}, "version", false},
		{"after -- terminator", []string{"send", "--", "--version"}, "version", false},
		{"drifted subcommand carries flag", []string{"unknown-future-subcmd", "--version"}, "version", true},
		{"json flag absent", []string{"--version"}, "json", false},
		{"json flag present", []string{"--version", "--json"}, "json", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := argvHasFlag(tt.args, tt.flag); got != tt.want {
				t.Errorf("argvHasFlag(%v, %q) = %v, want %v", tt.args, tt.flag, got, tt.want)
			}
		})
	}
}
