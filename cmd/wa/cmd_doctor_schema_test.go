package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestCheckSchemaVersion_MatchesLayoutConst is the #180 item-5 regression: a
// healthy install writes domain.LayoutSchemaVersion to .schema-version, and
// `wa doctor` must report OK — not the old hardcoded "expected v4" WARN that
// fired on every healthy v2 install.
func TestCheckSchemaVersion_MatchesLayoutConst(t *testing.T) {
	newXDGSandbox(t)
	want := strconv.Itoa(domain.LayoutSchemaVersion)
	writeSchemaFile(t, want)

	got := checkSchemaVersion()
	if got.Status != doctorOK {
		t.Fatalf("healthy v%s install: status = %q (%s), want OK", want, got.Status, got.Detail)
	}
	if got.Detail != "v"+want {
		t.Errorf("detail = %q, want %q", got.Detail, "v"+want)
	}
}

// TestCheckSchemaVersion_DriftWarns confirms a genuinely stale file still
// WARNs and reports the authoritative expected version, proving the message is
// derived from the shared constant rather than a hardcoded literal.
func TestCheckSchemaVersion_DriftWarns(t *testing.T) {
	newXDGSandbox(t)
	writeSchemaFile(t, "1") // pre-008 layout

	got := checkSchemaVersion()
	if got.Status != doctorWARN {
		t.Fatalf("stale v1 install: status = %q, want WARN", got.Status)
	}
	want := "expected v" + strconv.Itoa(domain.LayoutSchemaVersion)
	if !strings.Contains(got.Detail, want) {
		t.Errorf("detail = %q, want it to contain %q", got.Detail, want)
	}
}

func writeSchemaFile(t *testing.T, v string) {
	t.Helper()
	dir := filepath.Join(xdg.ConfigHome, "wa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".schema-version"), []byte(v+"\n"), 0o600); err != nil {
		t.Fatalf("write schema file: %v", err)
	}
}
