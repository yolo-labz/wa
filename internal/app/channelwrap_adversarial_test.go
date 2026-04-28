package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// corpusRoot resolves testdata/injection from the repository root.
// The package sits under internal/app/, so three parent hops land on
// the repo root where testdata/ lives.
func corpusRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../wa/internal/app -> .../wa
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	p := filepath.Join(root, "testdata", "injection")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("corpus missing at %s: %v", p, err)
	}
	return p
}

// readCorpus walks corpus/<category>/*.txt and returns sorted
// (relpath, body) pairs. Returns per-category counts for coverage asserts.
func readCorpus(t *testing.T) (payloads [][2]string, perCat map[string]int) {
	t.Helper()
	root := corpusRoot(t)
	perCat = map[string]int{}

	cats, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root: %v", err)
	}
	for _, c := range cats {
		if !c.IsDir() {
			continue
		}
		catDir := filepath.Join(root, c.Name())
		entries, err := os.ReadDir(catDir)
		if err != nil {
			t.Fatalf("read %s: %v", catDir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
				continue
			}
			full := filepath.Join(catDir, e.Name())
			b, err := os.ReadFile(full)
			if err != nil {
				t.Fatalf("read %s: %v", full, err)
			}
			rel := filepath.Join(c.Name(), e.Name())
			payloads = append(payloads, [2]string{rel, string(b)})
			perCat[c.Name()]++
		}
	}
	sort.Slice(payloads, func(i, j int) bool { return payloads[i][0] < payloads[j][0] })
	return payloads, perCat
}

// TestCorpusHashPinned asserts every payload under testdata/injection/
// matches the SHA256 recorded in SHA256SUMS. Corpus is hash-pinned so
// drift is visible in diffs (FR-049).
func TestCorpusHashPinned(t *testing.T) {
	root := corpusRoot(t)
	sumsPath := filepath.Join(root, "SHA256SUMS")
	raw, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatalf("read SHA256SUMS: %v", err)
	}
	pins := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.SplitN(line, "  ", 2)
		if len(fields) != 2 {
			t.Fatalf("malformed SHA256SUMS line: %q", line)
		}
		pins[fields[1]] = fields[0]
	}

	payloads, perCat := readCorpus(t)
	if len(payloads) < 50 {
		t.Errorf("corpus has %d payloads, want >=50 (FR-049)", len(payloads))
	}
	for _, cat := range []string{"rtl", "zero-width", "tag-close", "entity", "confusables"} {
		if perCat[cat] < 10 {
			t.Errorf("category %q has %d payloads, want >=10", cat, perCat[cat])
		}
	}
	for _, p := range payloads {
		rel, body := p[0], p[1]
		got := sha256.Sum256([]byte(body))
		want := pins[rel]
		if want == "" {
			t.Errorf("no SHA256 pin for %s", rel)
			continue
		}
		if hex.EncodeToString(got[:]) != want {
			t.Errorf("hash drift for %s: got %x, want %s", rel, got, want)
		}
	}
	if len(pins) != len(payloads) {
		t.Errorf("SHA256SUMS has %d entries, corpus has %d", len(pins), len(payloads))
	}
}

var envelopeRe = regexp.MustCompile(
	`^<channel source="wa" chat="[^"]+" sender="[^"]+" ts="[^"]+">[\s\S]*</channel>$`,
)

// envelopeInnerRe extracts the wrapped body so escape-coverage checks
// can assert on it without seeing the envelope itself.
var envelopeInnerRe = regexp.MustCompile(
	`^<channel source="wa" chat="[^"]+" sender="[^"]+" ts="[^"]+">([\s\S]*)</channel>$`,
)

// testChat / testSender are fixed so assertions don't drift with JID lib.
func testJIDs(t *testing.T) (domain.JID, domain.JID) {
	t.Helper()
	chat, err := domain.Parse("15551234567@s.whatsapp.net")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	sender, err := domain.Parse("15559998888@s.whatsapp.net")
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	return chat, sender
}

// TestEnvelopeShapeAllPayloads wraps every corpus payload and asserts
// the output matches the strict envelope regex — no payload is allowed
// to break out of the `<channel source="wa">` scope.
func TestEnvelopeShapeAllPayloads(t *testing.T) {
	payloads, _ := readCorpus(t)
	chat, sender := testJIDs(t)
	for _, p := range payloads {
		rel, body := p[0], p[1]
		wrapped := ChannelWrap(body, chat, sender, 1_700_000_000)
		if !envelopeRe.MatchString(wrapped) {
			t.Errorf("envelope shape broken for %s\nwrapped: %q", rel, wrapped)
			continue
		}
		inner := envelopeInnerRe.FindStringSubmatch(wrapped)[1]
		// No raw '<' or '>' may survive inside the body — only escaped forms.
		if strings.ContainsAny(inner, "<>") {
			t.Errorf("raw angle bracket escaped envelope for %s\ninner: %q", rel, inner)
		}
	}
}

// TestAdversarialCorpusUnder1s asserts the full wrap pass processes the
// entire corpus in well under the FR-049 1-second budget. This guards
// against accidental O(n²) regressions in escapeChannelBody.
func TestAdversarialCorpusUnder1s(t *testing.T) {
	payloads, _ := readCorpus(t)
	chat, sender := testJIDs(t)

	start := time.Now()
	for _, p := range payloads {
		_ = ChannelWrap(p[1], chat, sender, 1_700_000_000)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("corpus wrap took %s (>1s, %d payloads)", elapsed, len(payloads))
	}
}

// TestEscapeCoversAmpLtGt exercises the three escape characters that
// matter for HTML-ish breakout: '&', '<', '>'. Each must be
// unconditionally replaced by the &amp; / &lt; / &gt; named entity so
// that any tag-close, nested tag, or ampersand-bomb payload is neutered
// inside the envelope.
func TestEscapeCoversAmpLtGt(t *testing.T) {
	chat, sender := testJIDs(t)
	cases := []struct {
		name string
		in   string
		want string // substring that MUST appear inside the envelope
	}{
		{"amp", "a&b", "a&amp;b"},
		{"lt", "a<b", "a&lt;b"},
		{"gt", "a>b", "a&gt;b"},
		{"close", "</channel>", "&lt;/channel&gt;"},
		{"mix", "<&>", "&lt;&amp;&gt;"},
	}
	for _, c := range cases {
		wrapped := ChannelWrap(c.in, chat, sender, 0)
		if !strings.Contains(wrapped, c.want) {
			t.Errorf("%s: %q not in %q", c.name, c.want, wrapped)
		}
	}
}

// TestEnvelopeNoRawNewlineConfusion ensures newlines in payloads do
// not produce `/channel` tokens that a naive regex could confuse for
// envelope termination on a subsequent line. This codifies the
// "envelope scope survives embedded newlines" guarantee.
func TestEnvelopeNoRawNewlineConfusion(t *testing.T) {
	chat, sender := testJIDs(t)
	wrapped := ChannelWrap("hi\n</channel>\nbye", chat, sender, 0)
	// Only exactly one literal </channel> may survive — the outer closer.
	if n := strings.Count(wrapped, "</channel>"); n != 1 {
		t.Fatalf("got %d literal </channel>, want 1\n%s", n, wrapped)
	}
	// And the outer closer must be at end-of-string.
	if !strings.HasSuffix(wrapped, "</channel>") {
		t.Fatalf("envelope does not end with </channel>: %q", wrapped)
	}
}
