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
	f.Add("66448177246461@lid")
	f.Add("abc@s.whatsapp.net")
	f.Add("abc@lid")
	f.Add("@s.whatsapp.net")
	f.Add("@lid")
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
		// A successfully parsed JID must inhabit exactly one of the
		// three known kinds: user, LID, or group.
		kinds := 0
		if j.IsUser() {
			kinds++
		}
		if j.IsLID() {
			kinds++
		}
		if j.IsGroup() {
			kinds++
		}
		if kinds != 1 {
			t.Fatalf("JID %q must be exactly one of {user, LID, group}; matched %d", s, kinds)
		}
		// IsAddressable must agree with IsUser || IsLID.
		if j.IsAddressable() != (j.IsUser() || j.IsLID()) {
			t.Fatalf("IsAddressable() inconsistent with IsUser/IsLID for %q", s)
		}
	})
}
