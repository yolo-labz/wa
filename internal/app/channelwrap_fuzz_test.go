package app

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fuzzEnvelopeRe matches the envelope shape. Kept local to this file so
// the fuzz harness is self-contained and survives refactors in the
// adversarial golden test.
var fuzzEnvelopeRe = regexp.MustCompile(
	`^<channel source="wa" chat="[^"]+" sender="[^"]+" ts="-?\d+">[\s\S]*</channel>$`,
)

var fuzzEnvelopeInnerRe = regexp.MustCompile(
	`^<channel source="wa" chat="[^"]+" sender="[^"]+" ts="-?\d+">([\s\S]*)</channel>$`,
)

// FuzzChannelWrap asserts three invariants over arbitrary body inputs:
//  1. The output always matches the envelope regex (no input can break
//     out of the `<channel source="wa">` scope).
//  2. Exactly one literal `</channel>` survives — the outer closer.
//  3. No raw '<' or '>' may appear inside the wrapped body; all angle
//     brackets must be HTML-escaped (&lt; / &gt;).
//
// Seeds cover known adversarial shapes: plain close tag, embedded
// ampersands, RTL override, zero-width joiner, numeric entity, empty
// string, long repeated payload.
func FuzzChannelWrap(f *testing.F) {
	seeds := []string{
		"",
		"hello",
		"</channel>",
		"<script>alert(1)</script>",
		"&amp;&lt;&gt;",
		"\u202erighttoleft",
		"\u200dzwj-joined",
		"&#60;&#62;",
		"a\nb\r\nc\tend",
		strings.Repeat("</channel>", 32),
		"\x00\x01\x1f control",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	chat, err := domain.Parse("15551234567@s.whatsapp.net")
	if err != nil {
		f.Fatalf("chat parse: %v", err)
	}
	sender, err := domain.Parse("15559998888@s.whatsapp.net")
	if err != nil {
		f.Fatalf("sender parse: %v", err)
	}

	f.Fuzz(func(t *testing.T, body string) {
		wrapped := ChannelWrap(body, chat, sender, 1_700_000_000)

		if !fuzzEnvelopeRe.MatchString(wrapped) {
			t.Fatalf("envelope broken for %q\nwrapped: %q", body, wrapped)
		}
		if !utf8.ValidString(wrapped) {
			t.Fatalf("non-utf8 output for %q", body)
		}
		if n := strings.Count(wrapped, "</channel>"); n != 1 {
			t.Fatalf("got %d </channel>, want 1\nbody=%q wrapped=%q", n, body, wrapped)
		}
		if !strings.HasSuffix(wrapped, "</channel>") {
			t.Fatalf("envelope not suffixed by </channel>\nwrapped=%q", wrapped)
		}

		m := fuzzEnvelopeInnerRe.FindStringSubmatch(wrapped)
		if m == nil {
			t.Fatalf("inner regex did not match: %q", wrapped)
		}
		inner := m[1]
		if strings.ContainsAny(inner, "<>") {
			t.Fatalf("raw angle bracket in inner body: body=%q inner=%q", body, inner)
		}
	})
}
