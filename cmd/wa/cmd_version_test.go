package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/buildstamp"
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

// stampVersion sets the three build-stamp vars for one test and restores
// them afterwards. They are package-level because the linker writes them.
func stampVersion(t *testing.T, v, c, d string) {
	t.Helper()
	sv, sc, sd := version, commit, date
	t.Cleanup(func() { version, commit, date = sv, sc, sd })
	version, commit, date = v, c, d
}

// TestPrintVersion_JSONCarriesCommit — issue #365. Confirming a canary
// rollout has to be a SHA compare; a `git describe` string cannot answer
// "is the commit I pushed the one running". The wa binary could not report
// this at all before, because it declared no main.commit for the ldflag
// both build systems were already passing.
func TestPrintVersion_JSONCarriesCommit(t *testing.T) {
	stampVersion(t, "v2.3.0", "fc53038", "2026-09-07T20:00:00Z")

	var buf bytes.Buffer
	printVersion(&buf, true)
	got := buf.String()

	for _, want := range []string{
		`"schema":"wa.version/v1"`,
		`"version":"v2.3.0"`,
		`"commit":"fc53038"`,
		`"date":"2026-09-07T20:00:00Z"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %q", want, got)
		}
	}
}

// TestPrintVersion_JSONOmitsUnsetStamps — the fields are additive, so an
// unstamped build emits the byte-identical v1 shape it always did. That is
// what makes this a no-schema-bump change (FR-004).
func TestPrintVersion_JSONOmitsUnsetStamps(t *testing.T) {
	stampVersion(t, "v1.2.3", "", "")

	var buf bytes.Buffer
	printVersion(&buf, true)

	want := `{"schema":"wa.version/v1","version":"v1.2.3"}` + "\n"
	if got := buf.String(); got != want {
		t.Errorf("unstamped JSON drifted from the v1 shape:\n got %q\nwant %q", got, want)
	}
}

// TestPrintVersion_HumanShowsCommit — the operator reading a terminal gets
// the SHA too, not only the --json consumer.
func TestPrintVersion_HumanShowsCommit(t *testing.T) {
	stampVersion(t, "v2.3.0", "fc53038", "2026-09-07T20:00:00Z")

	var buf bytes.Buffer
	printVersion(&buf, false)

	want := "wa version v2.3.0 (fc53038 @ 2026-09-07T20:00:00Z)\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestVersionBannerPartials — a build with only some stamps must not
// render empty parens or a stray "@".
func TestVersionBannerPartials(t *testing.T) {
	t.Parallel()
	cases := []struct{ v, c, d, want string }{
		{"v1", "", "", "v1"},
		{"v1", "abc", "", "v1 (abc)"},
		{"v1", "", "2026-01-01", "v1 (@ 2026-01-01)"},
		{"v1", "abc", "2026-01-01", "v1 (abc @ 2026-01-01)"},
	}
	for _, tc := range cases {
		if got := buildstamp.Banner(tc.v, tc.c, tc.d); got != tc.want {
			t.Errorf("buildstamp.Banner(%q,%q,%q) = %q, want %q", tc.v, tc.c, tc.d, got, tc.want)
		}
	}
}
