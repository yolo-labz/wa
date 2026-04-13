package sqlitehistory

import "strings"

// SanitizeBody strips null bytes and C0 control characters
// (U+0000–U+001F) except horizontal tab (\t), newline (\n), and
// carriage return (\r) from a message body before storage. This
// prevents rendering issues in downstream consumers.
//
// Feature 009 — spec FR-038.
func SanitizeBody(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r >= 0 && r <= 0x1F {
			return -1 // strip
		}
		return r
	}, s)
}
