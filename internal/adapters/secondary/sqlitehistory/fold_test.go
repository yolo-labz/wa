package sqlitehistory

import "testing"

// TestFoldText covers the fold itself, separately from the query that uses it,
// because the fold is what makes a caption findable and a bug here is invisible
// from the outside: a search just returns nothing, which reads as "no such
// media" rather than "the needle and the caption were normalised differently".
// Issue #315.
func TestFoldText(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"ascii lowercases", "SEGUE", "segue"},
		{"plain ascii untouched", "catalogo", "catalogo"},
		// The #315 symptom, from both ends: an accented caption and an
		// unaccented needle have to land on the same string.
		{"accent stripped", "catálogo", "catalogo"},
		{"accent stripped and lowered", "CATÁLOGO", "catalogo"},
		{"mixed", "Segue nosso CATÁLOGO", "segue nosso catalogo"},
		// Portuguese carries more than the acute: all of these are one base
		// letter plus a combining mark once NFD-decomposed.
		{"tilde", "São Paulo", "sao paulo"},
		{"cedilla", "Alteração", "alteracao"},
		{"circumflex", "Você", "voce"},
		{"grave", "às", "as"},
		// Pre-composed and decomposed spellings of the same word arrive from
		// different keyboards and must not be two different search keys. The
		// literal below is the DECOMPOSED spelling (a + U+0301), byte-distinct
		// from the composed one above; if an editor ever NFC-normalises this
		// file the case collapses into a duplicate of it, so check the bytes
		// before trusting it still covers what it claims.
		{"decomposed input", "catálogo", "catalogo"},
		// A needle of only combining marks folds away entirely. The caller
		// treats "" as "no caption filter", which is why the backfill skips
		// these rows rather than indexing an empty key that matches nothing.
		{"marks only", "́̃", ""},
		// Non-Latin scripts have no marks to strip; the fold must not mangle
		// them into nothing.
		{"cyrillic", "Привет", "привет"},
		{"cjk untouched", "日本", "日本"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := foldText(tc.in); got != tc.want {
				t.Errorf("foldText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFoldTextIsIdempotent pins that folding a folded string is a no-op. The
// stored column is written once on insert and the needle is folded on every
// query; if the two disagreed after one extra pass, a re-run of the v8 backfill
// would silently rewrite rows into a form the query no longer matches.
func TestFoldTextIsIdempotent(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"Segue nosso CATÁLOGO", "São Paulo", "日本", ""} {
		once := foldText(in)
		if twice := foldText(once); twice != once {
			t.Errorf("foldText(foldText(%q)) = %q, want %q", in, twice, once)
		}
	}
}

// TestCaptionLikePattern escapes LIKE metacharacters. The wire param is a plain
// substring on purpose: if a client could pass "%" the server would happily
// turn a narrowing filter into a full dump. Moved here from cmd/wad with the
// function itself — the store owns the wildcards now, so no caller can build a
// pattern of its own.
func TestCaptionLikePattern(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		in, want string
	}{
		// An empty needle is "no caption filter", never `%%`. The query reads
		// the empty pattern as "skip this predicate"; widening to match-all
		// would turn an unset flag into a full dump.
		{"empty", "", ""},
		{"plain", "catalogo", "%catalogo%"},
		{"percent escaped", "100%", `%100\%%`},
		{"underscore escaped", "snapshot_1", `%snapshot\_1%`},
		{"backslash escaped", `back\slash`, `%back\\slash%`},
		// A caller who types "%" gets a search for a literal percent sign, not
		// a match-everything wildcard.
		{"bare percent", "%", `%\%%`},
		// The fold runs BEFORE the escape, so the pattern is folded and the
		// metacharacters around it are still literal.
		{"folded then escaped", "CATÁLOGO_1", `%catalogo\_1%`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := captionLikePattern(tc.in); got != tc.want {
				t.Errorf("captionLikePattern(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
