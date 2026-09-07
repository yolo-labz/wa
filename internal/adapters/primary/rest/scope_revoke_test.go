package rest

import (
	"encoding/json"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestRevokeScopeNarrowing pins the ONE param-aware narrowing in the scope
// gate: message.revoke with an explicit scope:"self" drops to ScopeSend,
// everything else keeps the admin bar.
//
// The failure this guards is privilege escalation, so the interesting cases
// are all the ways params can fail to say "self" — absent, null, wrong type,
// wrong case, padded, garbage. Every one of them MUST stay admin.
func TestRevokeScopeNarrowing(t *testing.T) {
	t.Parallel()
	const m = "message.revoke"
	cases := []struct {
		name   string
		params string
		want   MethodScope
	}{
		{"explicit self narrows to send", `{"chat":"c","messageId":"m","scope":"self"}`, ScopeSend},
		{"explicit everyone stays admin", `{"chat":"c","messageId":"m","scope":"everyone"}`, ScopeAdmin},

		// The dispatcher reads an omitted scope as "everyone"
		// (app.revokeParams). The gate MUST agree, or a send token would
		// broadcast a tombstone.
		{"omitted scope stays admin", `{"chat":"c","messageId":"m"}`, ScopeAdmin},
		{"empty params object stays admin", `{}`, ScopeAdmin},
		{"empty scope string stays admin", `{"scope":""}`, ScopeAdmin},
		{"null scope stays admin", `{"scope":null}`, ScopeAdmin},

		// domain.ParseRevokeScope is strict; the gate inherits that strictness
		// instead of re-implementing a looser compare.
		{"capitalised Self stays admin", `{"scope":"Self"}`, ScopeAdmin},
		{"upper SELF stays admin", `{"scope":"SELF"}`, ScopeAdmin},
		{"padded self stays admin", `{"scope":" self"}`, ScopeAdmin},
		{"trailing space stays admin", `{"scope":"self "}`, ScopeAdmin},
		{"unknown token stays admin", `{"scope":"nobody"}`, ScopeAdmin},

		// Anything that is not a JSON object of the expected shape.
		{"scope as number stays admin", `{"scope":3}`, ScopeAdmin},
		{"scope as object stays admin", `{"scope":{"x":"self"}}`, ScopeAdmin},
		{"params as array stays admin", `["self"]`, ScopeAdmin},
		{"params as bare string stays admin", `"self"`, ScopeAdmin},
		{"malformed json stays admin", `{"scope":"self"`, ScopeAdmin},
		{"json null stays admin", `null`, ScopeAdmin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := RequiredScope(m, json.RawMessage(tc.params))
			if !ok {
				t.Fatalf("RequiredScope(%q, %s) reported the method unknown", m, tc.params)
			}
			if got != tc.want {
				t.Errorf("RequiredScope(%q, %s) = %v, want %v", m, tc.params, got, tc.want)
			}
		})
	}
}

// TestRevokeNilParamsStaysAdmin — a caller with no params at all must not
// be narrowed. nil is the media.upload call site's argument, and it is also
// what any future caller gets wrong first.
func TestRevokeNilParamsStaysAdmin(t *testing.T) {
	t.Parallel()
	got, ok := RequiredScope("message.revoke", nil)
	if !ok || got != ScopeAdmin {
		t.Fatalf("RequiredScope(revoke, nil) = (%v, %v), want (admin, true)", got, ok)
	}
	if AllowedScope("message.revoke", nil, ScopeSend) {
		t.Error("a send token must not revoke without an explicit self scope")
	}
}

// TestNarrowingIsRevokeOnly — no other admin method may be talked down by
// carrying a scope field. A caller cannot smuggle `scope:"self"` into
// session.logout.
func TestNarrowingIsRevokeOnly(t *testing.T) {
	t.Parallel()
	smuggle := json.RawMessage(`{"scope":"self"}`)
	for method, want := range MethodScopes {
		if method == revokeMethod {
			continue
		}
		got, ok := RequiredScope(method, smuggle)
		if !ok {
			t.Errorf("%s: classified method reported unknown", method)
			continue
		}
		if got != want {
			t.Errorf("%s: scope:\"self\" changed the bar from %v to %v", method, want, got)
		}
	}
}

// TestGateAgreesWithDomainParser is the anti-drift property. The gate
// narrows exactly when domain.ParseRevokeScope — the same parser the
// dispatcher uses to decide what the call actually does — says RevokeSelf.
// If those two ever disagree, a send token performs an everyone-revoke.
func TestGateAgreesWithDomainParser(t *testing.T) {
	t.Parallel()
	tokens := []string{
		"self", "everyone", "Self", "SELF", " self", "self ", "",
		"nobody", "SELF ", "sElF", "self\n", "\tself",
	}
	for _, tok := range tokens {
		params, err := json.Marshal(map[string]string{"scope": tok})
		if err != nil {
			t.Fatalf("marshal %q: %v", tok, err)
		}
		got, _ := RequiredScope(revokeMethod, params)
		narrowed := got == ScopeSend

		sc, parseErr := domain.ParseRevokeScope(tok)
		domainSaysSelf := parseErr == nil && sc == domain.RevokeSelf

		if narrowed != domainSaysSelf {
			t.Errorf("scope %q: gate narrowed=%v but domain says self=%v", tok, narrowed, domainSaysSelf)
		}
	}
}
