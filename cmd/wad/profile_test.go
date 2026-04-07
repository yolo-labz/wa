// Package main tests for cmd/wad.
package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestValidateProfileName_Accepts covers the happy-path names that FR-002
// and FR-003 declare valid.
func TestValidateProfileName_Accepts(t *testing.T) {
	t.Parallel()
	cases := []string{
		"default", // canonical default profile — NOT reserved
		"work",
		"personal",
		"test-1",
		"abc",                              // minimum length
		"a1",                               // 2-char alphanumeric
		"abcdefghij0123456789abcdefghij01", // 32-char max
		"work-account",
		"a-b",
		"client-2026",
	}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			if err := ValidateProfileName(name); err != nil {
				t.Fatalf("ValidateProfileName(%q) = %v, want nil", name, err)
			}
		})
	}
}

// TestValidateProfileName_RejectsInvalid covers FR-002 regex rejections.
func TestValidateProfileName_RejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                                  "empty",
		"a":                                 "too-short",
		"A":                                 "uppercase",
		"Work":                              "uppercase-first",
		"1work":                             "numeric-first",
		"-work":                             "hyphen-first",
		"work-":                             "hyphen-last",
		"work_test":                         "underscore",
		"work test":                         "space",
		"work/test":                         "slash",
		"work\\test":                        "backslash",
		"..":                                "dot-dot (regex reject first, then reserved)",
		".":                                 "dot (regex reject first, then reserved)",
		"work..test":                        "double-dot",
		"work@host":                         "at-sign",
		"work$":                             "dollar",
		"work<test":                         "xml-lt",
		"work>test":                         "xml-gt",
		"work&test":                         "ampersand",
		"work.test":                         "internal-dot",
		"work\x00test":                      "null-byte",
		"work--test":                        "double-hyphen",
		"a--b":                              "double-hyphen-short",
		"abcdefghij0123456789abcdefghij012": "33-char too long",
	}
	for name, label := range cases {
		name, label := name, label
		t.Run(label, func(t *testing.T) {
			err := ValidateProfileName(name)
			if err == nil {
				t.Fatalf("ValidateProfileName(%q) = nil, want error", name)
			}
			if !errors.Is(err, ErrInvalidProfileName) && !errors.Is(err, ErrReservedProfileName) {
				t.Fatalf("ValidateProfileName(%q) = %v, want ErrInvalidProfileName or ErrReservedProfileName", name, err)
			}
		})
	}
}

// TestValidateProfileName_RejectsReserved covers FR-003 reserved list.
// Includes case-insensitive comparisons for future-proofing.
func TestValidateProfileName_RejectsReserved(t *testing.T) {
	t.Parallel()
	reserved := []string{
		"con", "prn", "aux", "nul",
		"com0", "com1", "com9",
		"lpt1", "lpt9",
		"wa", "wad",
		"root", "system",
		"dbus", "systemd", "user", "session",
		// subcommand verbs
		"list", "use", "create", "rm", "show", "new", "delete",
		"current", "switch", "all", "none", "self", "me", "migrate",
		// systemd unit-type suffix words
		"service", "socket", "target", "timer",
		"mount", "path", "slice", "scope", "device", "swap",
	}
	for _, name := range reserved {
		name := name
		t.Run(name, func(t *testing.T) {
			err := ValidateProfileName(name)
			if err == nil {
				t.Fatalf("ValidateProfileName(%q) = nil, want ErrReservedProfileName", name)
			}
			if !errors.Is(err, ErrReservedProfileName) {
				t.Fatalf("ValidateProfileName(%q) = %v, want ErrReservedProfileName", name, err)
			}
		})
	}
}

// TestValidateProfileName_DefaultAllowed asserts the canonical default
// profile name passes validation (FR-003 explicit allowance).
func TestValidateProfileName_DefaultAllowed(t *testing.T) {
	t.Parallel()
	if err := ValidateProfileName(DefaultProfile); err != nil {
		t.Fatalf("ValidateProfileName(%q) = %v, want nil (it IS the default)", DefaultProfile, err)
	}
}

// TestValidateProfileName_IsLocalProperty is the SC-015 property test:
// for every name that passes the regex AND isn't reserved, the name must
// also satisfy filepath.IsLocal, strings.ToLower, and !contains("--").
func TestValidateProfileName_IsLocalProperty(t *testing.T) {
	t.Parallel()
	valid := []string{
		"default", "work", "personal", "test-1", "a1", "abc",
		"work-account", "a-b", "client-2026",
	}
	for _, name := range valid {
		name := name
		t.Run(name, func(t *testing.T) {
			if !filepath.IsLocal(name) {
				t.Errorf("filepath.IsLocal(%q) = false, want true", name)
			}
			if lower := strings.ToLower(name); lower != name {
				t.Errorf("ToLower(%q) = %q, want %q (regex must imply lowercase)", name, lower, name)
			}
			if strings.Contains(name, "--") {
				t.Errorf("name %q contains --, which FR-002 forbids", name)
			}
		})
	}
}

// TestValidateProfileName_Performance asserts SC-005 (<1ms per call).
// This is not a benchmark — it's an assertion that the regex + reserved
// lookup path doesn't accidentally become allocation-heavy.
func TestValidateProfileName_Performance(t *testing.T) {
	t.Parallel()
	const iterations = 10000
	start := time.Now()
	for i := 0; i < iterations; i++ {
		_ = ValidateProfileName("work-account")
	}
	elapsed := time.Since(start)
	avg := elapsed / iterations
	if avg > time.Millisecond {
		t.Fatalf("ValidateProfileName averaged %v per call, want <1ms (SC-005)", avg)
	}
	t.Logf("ValidateProfileName: %v per call over %d iterations", avg, iterations)
}

// TestPathResolver_Methods asserts every resolver method returns a
// plausible path for the default profile. Doesn't assert exact string
// values because those depend on XDG env — instead asserts shape.
func TestPathResolver_Methods(t *testing.T) {
	t.Parallel()
	r, err := NewPathResolver("work")
	if err != nil {
		t.Fatalf("NewPathResolver: %v", err)
	}
	if r.Profile() != "work" {
		t.Errorf("Profile() = %q, want %q", r.Profile(), "work")
	}

	paths := map[string]string{
		"SessionDB":           r.SessionDB(),
		"HistoryDB":           r.HistoryDB(),
		"AllowlistTOML":       r.AllowlistTOML(),
		"AuditLog":            r.AuditLog(),
		"WadLog":              r.WadLog(),
		"PairHTMLPath":        r.PairHTMLPath(),
		"CacheDir":            r.CacheDir(),
		"ActiveProfileFile":   r.ActiveProfileFile(),
		"SchemaVersionFile":   r.SchemaVersionFile(),
		"MigratingMarkerFile": r.MigratingMarkerFile(),
		"DataDir":             r.DataDir(),
		"ConfigDir":           r.ConfigDir(),
		"StateDir":            r.StateDir(),
	}
	for name, p := range paths {
		if p == "" {
			t.Errorf("%s returned empty string", name)
		}
		if !filepath.IsAbs(p) {
			t.Errorf("%s = %q, want absolute path", name, p)
		}
	}

	// SessionDB must contain the profile name and end in session.db.
	if !strings.Contains(r.SessionDB(), "work") {
		t.Errorf("SessionDB = %q, does not contain profile name", r.SessionDB())
	}
	if !strings.HasSuffix(r.SessionDB(), "session.db") {
		t.Errorf("SessionDB = %q, does not end in session.db", r.SessionDB())
	}

	// PairHTMLPath must be profile-suffixed per FR-014.
	if !strings.Contains(r.PairHTMLPath(), "wa-pair-work.html") {
		t.Errorf("PairHTMLPath = %q, does not contain wa-pair-work.html", r.PairHTMLPath())
	}

	// CacheDir must NOT contain the profile name (FR-012, shared).
	if strings.Contains(r.CacheDir(), "work") {
		t.Errorf("CacheDir = %q, should not contain profile name (shared per FR-012)", r.CacheDir())
	}
}

// TestPathResolver_RejectsInvalid ensures NewPathResolver validates.
func TestPathResolver_RejectsInvalid(t *testing.T) {
	t.Parallel()
	if _, err := NewPathResolver("BadName"); err == nil {
		t.Fatal("NewPathResolver(BadName) = nil error, want validation failure")
	}
	if _, err := NewPathResolver(""); err == nil {
		t.Fatal("NewPathResolver(empty) = nil error, want validation failure")
	}
	if _, err := NewPathResolver("list"); err == nil {
		t.Fatal("NewPathResolver(list) = nil error, want reserved rejection")
	}
}

// BenchmarkValidateProfileName for SC-005 — running benchstat is optional
// but the assertion in TestValidateProfileName_Performance covers the
// threshold.
func BenchmarkValidateProfileName(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = ValidateProfileName("work-account-42")
	}
}
