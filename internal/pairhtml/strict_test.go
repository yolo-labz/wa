package pairhtml

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

// TestRender_StrictCSPMode (FR-116-02, R4): the admin render mode needs
// nothing a `default-src 'self'` policy without 'unsafe-inline' blocks —
// no style element, no inline style, no data: URI, no script — in every
// state, and links the stylesheet and same-origin QR image it is given.
func TestRender_StrictCSPMode(t *testing.T) {
	opts := Strict{Stylesheet: "/admin/static/admin.css", QRURL: "/admin/pair/n0nce/qr.png", Refresh: 5}
	banned := regexp.MustCompile(`(?i)<style|style=|data:|<script|javascript:|https?://`)
	for _, state := range []State{StateLoading, StateQR, StatePaired} {
		doc, err := RenderStrict(state, opts)
		if err != nil {
			t.Fatalf("state %d: %v", state, err)
		}
		if m := banned.FindString(doc); m != "" {
			t.Errorf("state %d: strict document contains %q", state, m)
		}
		if !strings.Contains(doc, `<link rel="stylesheet" href="/admin/static/admin.css">`) {
			t.Errorf("state %d: stylesheet not linked", state)
		}
	}
	doc, _ := RenderStrict(StateQR, opts)
	if !strings.Contains(doc, `<img src="/admin/pair/n0nce/qr.png" alt="QR code">`) ||
		!strings.Contains(doc, `<meta http-equiv="refresh" content="5">`) {
		t.Errorf("QR state lacks the same-origin image or its refresh:\n%s", doc)
	}
	if _, err := RenderStrict(StateQR, Strict{Stylesheet: opts.Stylesheet}); err == nil {
		t.Error("QR state without an image URL rendered")
	}
}

// TestRender_LegacyUnchanged: the CLI's Render output is byte-identical
// to its pre-S6 form (spec: the CLI path keeps its behaviour).
func TestRender_LegacyUnchanged(t *testing.T) {
	want := map[State]string{
		StateLoading: "ca0dea38360439a4c3e0ec9fee84b39ade25f8788188cd2b96f3da7d4333338d",
		StateQR:      "d60a3f17539a468733f2170ea07199775b2e8021c406bc2af51ac8961333bd05",
		StatePaired:  "7351ff5aa40e5f96b6981981e830c822b279134b1b7fe8525abd01e62423ef25",
	}
	for state, sum := range want {
		doc, err := Render(state, "QkFTRTY0")
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256([]byte(doc))
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("state %d: legacy document changed (sha256 %x)", state, got)
		}
	}
}
