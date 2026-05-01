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
		// Issue: LinkedIn-click-to-WA contacts arrive as LIDs. The
		// 14-digit fixture below is the exact shape reported by
		// Pedro on 2026-04-30 ("66448177246461@lid"), with the user
		// part replaced by digit fillers for the test corpus.
		{"canonical_lid", "66448177246461@lid", "66448177246461@lid", nil},
		{"long_lid", "12345678901234567890@lid", "12345678901234567890@lid", nil},
		{"lid_non_digit_user", "abc@lid", "", ErrInvalidJID},
		{"lid_empty_user", "@lid", "", ErrInvalidJID},
		// Spec 108 — additional addressable namespaces.
		{"canonical_hosted", "5511999999999@hosted", "5511999999999@hosted", nil},
		{"canonical_hosted_lid", "66448177246461@hosted.lid", "66448177246461@hosted.lid", nil},
		{"hosted_non_digit", "abc@hosted", "", ErrInvalidJID},
		{"canonical_bot", "13135550002@bot", "13135550002@bot", nil},
		{"bot_non_digit", "abc@bot", "", ErrInvalidJID},
		{"canonical_newsletter", "120363042199654321@newsletter", "120363042199654321@newsletter", nil},
		{"newsletter_with_hyphen", "120363-42199654321@newsletter", "120363-42199654321@newsletter", nil},
		// Broadcast lists are forbidden by safety policy. Distinct
		// sentinel so callers can branch.
		{"broadcast_refused", "12345@broadcast", "", ErrBroadcastForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
		"66448177246461@lid",
		"5511999999999@hosted",
		"66448177246461@hosted.lid",
		"13135550002@bot",
		"120363042199654321@newsletter",
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
	lid := MustJID("66448177246461@lid")
	hosted := MustJID("5511999999999@hosted")
	hostedLID := MustJID("66448177246461@hosted.lid")
	bot := MustJID("13135550002@bot")
	channel := MustJID("120363042199654321@newsletter")
	var zero JID
	if !user.IsUser() || user.IsGroup() || user.IsLID() {
		t.Error("user JID discriminator wrong")
	}
	if !group.IsGroup() || group.IsUser() || group.IsLID() {
		t.Error("group JID discriminator wrong")
	}
	if !lid.IsLID() || lid.IsUser() || lid.IsGroup() {
		t.Error("LID JID discriminator wrong")
	}
	if !hosted.IsHosted() || hosted.IsUser() || hosted.IsLID() {
		t.Error("hosted JID discriminator wrong")
	}
	if !hostedLID.IsHosted() || !hostedLID.IsAddressable() || hostedLID.IsLID() {
		t.Error("hosted.lid JID discriminator wrong")
	}
	if !bot.IsBot() || bot.IsUser() {
		t.Error("bot JID discriminator wrong")
	}
	if !channel.IsChannel() || channel.IsAddressable() || channel.IsGroup() {
		t.Error("newsletter (channel) JID discriminator wrong")
	}
	for _, j := range []JID{user, lid, hosted, hostedLID, bot} {
		if !j.IsAddressable() {
			t.Errorf("%v should be IsAddressable", j)
		}
	}
	if group.IsAddressable() {
		t.Error("group JID must not be IsAddressable")
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
	if lid.Server() != "lid" {
		t.Errorf("LID Server()=%q want %q", lid.Server(), "lid")
	}
	// PN and LID with the same user digits must be distinct values.
	pnSameDigits := MustJID("66448177246461")
	if pnSameDigits == lid {
		t.Error("PN and LID with same user digits must NOT be equal — separate namespaces")
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
	for range 8 {
		wg.Go(func() {
			for range 200 {
				if _, err := Parse("5511999999999"); err != nil {
					t.Errorf("parallel Parse failed: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
}
