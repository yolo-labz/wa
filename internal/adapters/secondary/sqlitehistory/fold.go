package sqlitehistory

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// foldText normalises a string for accent- and case-insensitive substring
// matching: NFD-decompose so every accented rune splits into a base letter
// plus a combining mark, drop the marks, recompose, then lowercase.
//
// It exists because SQLite's LIKE folds ASCII case ONLY. Against a caption
// reading "Segue nosso catálogo", the shouted "CATÁLOGO" missed (the accented
// rune has no fold) and so did the unaccented "catalogo" a phone keyboard
// produces — an empty result that reads as "no such media" rather than "you
// typed it without the accent". Issue #315.
//
// Both the stored caption and the search substring go through this, so the
// comparison is between two folded forms and neither side has to be spelled
// the way the other was.
//
// The fold is deliberately lossy and one-way. It is a search key, never a
// display value: `caption` keeps the bytes the sender actually wrote and is
// what every read path returns.
func foldText(s string) string {
	if s == "" {
		return ""
	}
	// runes.Remove(runes.In(unicode.Mn)) drops the combining marks NFD just
	// exposed; NFC puts the survivors back into composed form so a caption
	// with no accents at all is byte-identical to its input.
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		// transform.String on these transformers fails only on malformed
		// input. A caption is whatever bytes arrived over the wire, so this
		// is reachable; lowercasing the original still beats indexing "" and
		// silently matching nothing.
		return strings.ToLower(s)
	}
	return strings.ToLower(folded)
}

// captionLikePattern turns a plain substring into the folded SQL LIKE pattern
// matched against messages.caption_folded.
//
// The wildcards are the store's to add, never the caller's: a caller that
// built its own pattern could widen a caption filter to `%` and turn it into
// a full dump. Literal `%`, `_` and `\` in the substring are escaped so a
// caption containing "100%" or "snapshot_1" matches literally, which is why
// the predicate carries ESCAPE '\'.
//
// Returns "" for an empty substring, which the query reads as "no caption
// filter" — an empty needle must not become `%%` and match every row.
func captionLikePattern(sub string) string {
	if sub == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(foldText(sub)) + "%"
}
