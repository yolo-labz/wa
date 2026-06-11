package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"
)

// FuzzCanonicalJSON asserts the fingerprinting contract (FR-030..FR-033)
// over arbitrary JSON documents:
//
//  1. canonicalJSON never fails and always emits valid JSON for any
//     value that encoding/json itself accepts.
//  2. Deterministic: two calls over the same value are byte-identical
//     (map iteration order must never leak into the fingerprint).
//  3. Fixed point: re-parsing the canonical form and canonicalising
//     again reproduces the same bytes — sorting and reserved-field
//     stripping are idempotent.
//  4. CanonicalParamsHash equals sha256 of the canonical bytes, so the
//     idempotency store and any external auditor agree on the digest.
//  5. The input value is not mutated (top-level shallow copy holds).
//
// Seeds cover the reserved-field strip, nested maps, arrays, numeric
// edge values, unicode escapes, and duplicate keys.
func FuzzCanonicalJSON(f *testing.F) {
	seeds := []string{
		`null`,
		`{}`,
		`[]`,
		`true`,
		`-0`,
		`1e+21`,
		`"plain"`,
		`{"b":1,"a":2}`,
		`{"idempotencyKey":"k","requestId":"r","ts":1,"timestamp":2,"kept":true}`,
		`{"outer":{"idempotencyKey":"nested keys are kept"}}`,
		`[{"z":null},{"a":[1,2,3]},"x",false]`,
		`{"u":"\u202e\u200d mixed"}`,
		`{"dup":1,"dup":2}`,
		`{"big":123456789012345678901234567890}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, doc string) {
		var v any
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			t.Skip("not valid JSON")
		}

		before, err := json.Marshal(v)
		if err != nil {
			t.Skip("encoding/json cannot re-marshal input")
		}

		out, err := canonicalJSON(v)
		if err != nil {
			t.Fatalf("canonicalJSON failed on valid JSON %q: %v", doc, err)
		}
		if !json.Valid(out) {
			t.Fatalf("canonical output is not valid JSON: %q", out)
		}

		again, err := canonicalJSON(v)
		if err != nil {
			t.Fatalf("second canonicalJSON failed: %v", err)
		}
		if !bytes.Equal(out, again) {
			t.Fatalf("non-deterministic canonical form:\n first=%q\nsecond=%q", out, again)
		}

		var reparsed any
		if err := json.Unmarshal(out, &reparsed); err != nil {
			t.Fatalf("canonical output does not re-parse: %v\nout=%q", err, out)
		}
		fixed, err := canonicalJSON(reparsed)
		if err != nil {
			t.Fatalf("canonicalJSON of canonical form failed: %v", err)
		}
		if !bytes.Equal(out, fixed) {
			t.Fatalf("canonical form is not a fixed point:\n out=%q\nfixed=%q", out, fixed)
		}

		sum, err := CanonicalParamsHash(v)
		if err != nil {
			t.Fatalf("CanonicalParamsHash failed: %v", err)
		}
		if want := sha256.Sum256(out); sum != want {
			t.Fatalf("hash mismatch: CanonicalParamsHash != sha256(canonicalJSON)")
		}

		after, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("re-marshal of input failed: %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("canonicalJSON mutated its input:\nbefore=%q\n after=%q", before, after)
		}
	})
}
