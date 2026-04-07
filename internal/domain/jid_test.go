package domain

import (
	"errors"
	"sync"
	"testing"
)

func TestParse_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"empty", "", "", ErrInvalidJID},
		{"plus_spaces_parens_hyphens", "+55 (11) 99999-9999", "5511999999999@s.whatsapp.net", nil},
		{"canonical_user", "5511999999999@s.whatsapp.net", "5511999999999@s.whatsapp.net", nil},
		{"digits_only", "5511999999999", "5511999999999@s.whatsapp.net", nil},
		{"plus_prefix", "+5511999999999", "5511999999999@s.whatsapp.net", nil},
		{"canonical_group", "120363042199654321@g.us", "120363042199654321@g.us", nil},
		{"group_with_hyphen", "120363-42199654321@g.us", "120363-42199654321@g.us", nil},
		{"invalid_server", "5511999999999@invalid.server", "", ErrInvalidJID},
		{"non_digit_user", "abc@s.whatsapp.net", "", ErrInvalidJID},
		{"phone_too_short", "1234567", "", ErrInvalidPhone},
		{"phone_too_long", "1234567890123456", "", ErrInvalidPhone},
		{"two_at_symbols", "5511@foo@s.whatsapp.net", "", ErrInvalidJID},
		{"empty_user_in_jid", "@s.whatsapp.net", "", ErrInvalidJID},
		{"group_user_no_digit", "---@g.us", "", ErrInvalidJID},
		{"group_user_letters", "abc@g.us", "", ErrInvalidJID},
		{"seven_digits", "12345678", "12345678@s.whatsapp.net", nil},
		{"fifteen_digits", "123456789012345", "123456789012345@s.whatsapp.net", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j, err := Parse(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want err %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := j.String(); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParsePhone_Range(t *testing.T) {
	t.Parallel()
	if _, err := ParsePhone(""); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("empty phone: want ErrInvalidPhone, got %v", err)
	}
	if _, err := ParsePhone("1234567"); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("7-digit phone: want ErrInvalidPhone, got %v", err)
	}
	if _, err := ParsePhone("1234567890123456"); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("16-digit phone: want ErrInvalidPhone, got %v", err)
	}
}

func TestJID_RoundTrip(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"5511999999999@s.whatsapp.net",
		"120363042199654321@g.us",
	}
	for _, in := range inputs {
		j, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		j2, err := Parse(j.String())
		if err != nil {
			t.Fatalf("Parse(String) for %q: %v", in, err)
		}
		if j != j2 {
			t.Errorf("round-trip mismatch: %v != %v", j, j2)
		}
	}
}

func TestJID_Discriminators(t *testing.T) {
	t.Parallel()
	user := MustJID("5511999999999")
	group := MustJID("120363042199654321@g.us")
	var zero JID
	if !user.IsUser() || user.IsGroup() {
		t.Error("user JID discriminator wrong")
	}
	if !group.IsGroup() || group.IsUser() {
		t.Error("group JID discriminator wrong")
	}
	if !zero.IsZero() {
		t.Error("zero JID.IsZero should be true")
	}
	if user.IsZero() {
		t.Error("user.IsZero should be false")
	}
	if user.User() != "5511999999999" {
		t.Errorf("User()=%q", user.User())
	}
	if group.Server() != "g.us" {
		t.Errorf("Server()=%q", group.Server())
	}
}

func TestMustJID_Panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("MustJID(invalid) should panic")
		}
	}()
	_ = MustJID("")
}

func TestParse_Concurrent(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if _, err := Parse("5511999999999"); err != nil {
					t.Errorf("parallel Parse failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
