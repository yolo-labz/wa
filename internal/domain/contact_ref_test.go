package domain

import (
	"errors"
	"testing"
)

func TestContactRefExactlyOne(t *testing.T) {
	jid, err := Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatalf("Parse JID: %v", err)
	}

	cases := []struct {
		name     string
		ref      ContactRef
		wantKind ContactRefKind
		wantErr  error
	}{
		{"jid only", NewJIDRef(jid), ContactRefJID, nil},
		{"phone only +E164", NewPhoneRef("+5511999999999"), ContactRefPhone, nil},
		{"phone only bare digits", NewPhoneRef("5511999999999"), ContactRefPhone, nil},
		{"name only", NewNameRef("Alice"), ContactRefName, nil},
		{"all zero", ContactRef{}, 0, ErrContactRefShape},
		{"jid + phone", ContactRef{JID: jid, Phone: "+5511999999999"}, 0, ErrContactRefShape},
		{"jid + name", ContactRef{JID: jid, Name: "Alice"}, 0, ErrContactRefShape},
		{"phone + name", ContactRef{Phone: "+5511999999999", Name: "Alice"}, 0, ErrContactRefShape},
		{"all three", ContactRef{JID: jid, Phone: "+5511999999999", Name: "Alice"}, 0, ErrContactRefShape},
		{"phone too short", NewPhoneRef("+12345"), ContactRefPhone, ErrInvalidPhone},
		{"phone too long", NewPhoneRef("+1234567890123456"), ContactRefPhone, ErrInvalidPhone},
		{"phone non-digit", NewPhoneRef("+5511ABCD9999"), ContactRefPhone, ErrInvalidPhone},
		{"name whitespace-only", NewNameRef("   "), 0, ErrContactRefShape},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotKind := tc.ref.Kind()
			if tc.wantErr == nil {
				if gotKind != tc.wantKind {
					t.Fatalf("Kind = %v, want %v", gotKind, tc.wantKind)
				}
				if err := tc.ref.Validate(); err != nil {
					t.Fatalf("Validate returned %v, want nil", err)
				}
				return
			}
			err := tc.ref.Validate()
			if err == nil {
				t.Fatalf("Validate returned nil, want %v", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate returned %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

func TestContactRefKindString(t *testing.T) {
	pairs := []struct {
		k    ContactRefKind
		want string
	}{
		{ContactRefJID, "jid"},
		{ContactRefPhone, "phone"},
		{ContactRefName, "name"},
		{ContactRefKind(0), "unknown"},
	}
	for _, p := range pairs {
		if got := p.k.String(); got != p.want {
			t.Errorf("ContactRefKind(%d).String() = %q, want %q", p.k, got, p.want)
		}
	}
}
