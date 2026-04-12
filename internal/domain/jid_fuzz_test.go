package domain

import "testing"

// FuzzParse exercises the JID parser with arbitrary input to catch panics,
// infinite loops, and round-trip invariant violations. This also satisfies
// the OpenSSF Scorecard "Fuzzing" check.
func FuzzParse(f *testing.F) {
	// Seed corpus — representative valid and invalid inputs.
	f.Add("")
	f.Add("+5511999999999")
	f.Add("5511999999999")
	f.Add("5511999999999@s.whatsapp.net")
	f.Add("120363042199654321@g.us")
	f.Add("120363-42199654321@g.us")
	f.Add("abc@s.whatsapp.net")
	f.Add("@s.whatsapp.net")
	f.Add("5511@foo@bar")
	f.Add("---@g.us")
	f.Add("1234567")
	f.Add("1234567890123456")
	f.Add("+1 (555) 123-4567")

	f.Fuzz(func(t *testing.T, input string) {
		j, err := Parse(input)
		if err != nil {
			return // parse rejections are fine
		}

		// Round-trip invariant: Parse(j.String()) must succeed and
		// produce an identical JID.
		s := j.String()
		j2, err := Parse(s)
		if err != nil {
			t.Fatalf("round-trip failed: Parse(%q) ok, String()=%q, re-parse err: %v", input, s, err)
		}
		if j != j2 {
			t.Fatalf("round-trip mismatch: Parse(%q)=%v, Parse(%q)=%v", input, j, s, j2)
		}

		// Accessor sanity.
		if j.IsZero() {
			t.Fatal("successfully parsed JID should not be zero")
		}
		if j.IsUser() == j.IsGroup() {
			t.Fatal("JID must be exactly one of user or group")
		}
	})
}
