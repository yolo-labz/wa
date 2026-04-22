package app

import (
	"fmt"
	"strings"

	"github.com/yolo-labz/wa/internal/domain"
)

// ChannelWrap returns body wrapped in a `<channel source="wa" ...>` block
// mirroring the anthropics/claude-plugins-official Telegram plugin format
// (external_plugins/telegram/server.ts:371). The tag makes attacker-controlled
// text structurally distinguishable from trusted input when surfaced to an
// LLM. Angle brackets inside body are HTML-escaped to prevent nested-tag
// injection.
//
// Single-field entry-point kept for existing callers that carry only a
// message body/snippet. New code that carries multiple attacker-controlled
// fields MUST use ChannelWrapFields instead so each field is isolated
// inside its own `<field name="…">…</field>` child (FR-005a).
func ChannelWrap(body string, chat, sender domain.JID, ts int64) string {
	return fmt.Sprintf(
		`<channel source="wa" chat=%q sender=%q ts="%d">%s</channel>`,
		chat.String(), sender.String(), ts, escapeChannelBody(body),
	)
}

// escapeChannelBody replaces C0 control bytes (0x00–0x1F except
// \t \n \r) with U+FFFD and HTML-escapes `&`, `<`, `>` so a body
// containing `</channel>` cannot break out of its wrapper. Unicode
// confusables (U+202E, zero-width chars) are left untouched: the
// `<channel>` envelope is the trust boundary, escape tokens not
// Unicode (FR-005a).
func escapeChannelBody(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r >= 0 && r <= 0x1F {
			return '\uFFFD'
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
