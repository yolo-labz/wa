package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestIdempotencyKeyValidation(t *testing.T) {
	fp := ComputeFingerprint([]byte(`{"to":"x"}`))
	cases := []struct {
		name    string
		value   string
		fp      [32]byte
		wantErr error
	}{
		{"ok", "ik_01HM", fp, nil},
		{"max len 64", strings.Repeat("a", 64), fp, nil},
		{"empty value", "", fp, ErrIdempotencyKey},
		{"over 64", strings.Repeat("a", 65), fp, ErrIdempotencyKey},
		{"bad char", "ik 01", fp, ErrIdempotencyKey},
		{"zero fp", "ik_01HM", [32]byte{}, ErrIdempotencyKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, err := NewIdempotencyKey(tc.value, tc.fp)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("NewIdempotencyKey returned %v, want nil", err)
				}
				if err := k.Validate(); err != nil {
					t.Fatalf("Validate returned %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NewIdempotencyKey err = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

func TestIdempotencyFingerprintStable(t *testing.T) {
	// Canonical JSON for a given map is deterministic under json.Marshal
	// because Go sorts map keys alphabetically. Both orderings below MUST
	// therefore produce the same SHA-256.
	params1 := map[string]any{
		"to":          "5511999999999@s.whatsapp.net",
		"body":        "hello",
		"linkPreview": true,
	}
	params2 := map[string]any{
		"linkPreview": true,
		"body":        "hello",
		"to":          "5511999999999@s.whatsapp.net",
	}

	j1, err := json.Marshal(params1)
	if err != nil {
		t.Fatalf("json.Marshal params1: %v", err)
	}
	j2, err := json.Marshal(params2)
	if err != nil {
		t.Fatalf("json.Marshal params2: %v", err)
	}

	if string(j1) != string(j2) {
		t.Fatalf("canonical JSON is not stable across insertion order:\n  j1=%s\n  j2=%s", j1, j2)
	}

	fp1 := ComputeFingerprint(j1)
	fp2 := ComputeFingerprint(j2)
	if fp1 != fp2 {
		t.Fatalf("fingerprint not stable across reorderings: fp1=%x fp2=%x", fp1, fp2)
	}

	// Different body must produce a different fingerprint.
	params3 := map[string]any{
		"to":          "5511999999999@s.whatsapp.net",
		"body":        "hello2",
		"linkPreview": true,
	}
	j3, err := json.Marshal(params3)
	if err != nil {
		t.Fatalf("json.Marshal params3: %v", err)
	}
	fp3 := ComputeFingerprint(j3)
	if fp1 == fp3 {
		t.Fatalf("fingerprint collision across distinct payloads")
	}
}

func TestIdempotencyKeyMatches(t *testing.T) {
	fp := ComputeFingerprint([]byte(`{"a":1}`))
	k1, err := NewIdempotencyKey("ik_01", fp)
	if err != nil {
		t.Fatalf("NewIdempotencyKey k1: %v", err)
	}
	k2, err := NewIdempotencyKey("ik_01", fp)
	if err != nil {
		t.Fatalf("NewIdempotencyKey k2: %v", err)
	}
	if !k1.Matches(k2) {
		t.Fatalf("same value+fp should Match")
	}

	fp2 := ComputeFingerprint([]byte(`{"a":2}`))
	k3, err := NewIdempotencyKey("ik_01", fp2)
	if err != nil {
		t.Fatalf("NewIdempotencyKey k3: %v", err)
	}
	if k1.Matches(k3) {
		t.Fatalf("same value, different fp must NOT Match (triggers ErrIdempotencyConflict upstream)")
	}
}
