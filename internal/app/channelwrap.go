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
// Feature 017 — spec FR-099 (prompt-injection firewall).
func ChannelWrap(body string, chat, sender domain.JID, ts int64) string {
	return fmt.Sprintf(
		`<channel source="wa" chat=%q sender=%q ts="%d">%s</channel>`,
		chat.String(), sender.String(), ts, escapeChannelBody(body),
	)
}

// escapeChannelBody strips control characters and HTML-escapes angle brackets
// so a body containing `</channel>` cannot break out of its wrapper.
func escapeChannelBody(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r >= 0 && r <= 0x1F {
			return -1
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
