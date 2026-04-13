package domain

import (
	"fmt"
	"strings"
)

const (
	serverUser  = "s.whatsapp.net"
	serverGroup = "g.us"

	// minPhoneDigits and maxPhoneDigits define the ITU-T E.164 digit
	// range for international phone numbers (excluding country code
	// length variation).
	minPhoneDigits = 8
	maxPhoneDigits = 15
)

// JID is a WhatsApp Jabber-style identifier. The fields are unexported so
// that callers cannot construct an invalid JID from outside the package;
// the only valid constructors are Parse, ParsePhone, and MustJID.
type JID struct {
	user   string
	server string
}

// Parse accepts a phone-shaped input ("+5511...", "5511..."), a canonical
// user JID ("5511...@s.whatsapp.net"), or a canonical group JID
// ("120363...@g.us"), and returns the corresponding JID.
func Parse(input string) (JID, error) {
	if input == "" {
		return JID{}, fmt.Errorf("%w: empty input", ErrInvalidJID)
	}
	if strings.Contains(input, "@") {
		return parseJIDForm(input)
	}
	return ParsePhone(input)
}

func parseJIDForm(input string) (JID, error) {
	parts := strings.Split(input, "@")
	if len(parts) != 2 {
		return JID{}, fmt.Errorf("%w: %q", ErrInvalidJID, input)
	}
	user, server := parts[0], parts[1]
	if user == "" {
		return JID{}, fmt.Errorf("%w: empty user in %q", ErrInvalidJID, input)
	}
	switch server {
	case serverUser:
		if !allDigits(user) {
			return JID{}, fmt.Errorf("%w: non-digit user in %q", ErrInvalidJID, input)
		}
	case serverGroup:
		if !groupUserOK(user) {
			return JID{}, fmt.Errorf("%w: invalid group user in %q", ErrInvalidJID, input)
		}
	default:
		return JID{}, fmt.Errorf("%w: unknown server %q", ErrInvalidJID, server)
	}
	return JID{user: user, server: server}, nil
}

// ParsePhone normalises a phone string by stripping every non-digit byte
// and validates the resulting length is in the ITU-T E.164 [8,15] range.
func ParsePhone(phone string) (JID, error) {
	if phone == "" {
		return JID{}, fmt.Errorf("%w: empty input", ErrInvalidPhone)
	}
	var b strings.Builder
	b.Grow(len(phone))
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) < minPhoneDigits || len(digits) > maxPhoneDigits {
		return JID{}, fmt.Errorf("%w: %q normalised to %d digits", ErrInvalidPhone, phone, len(digits))
	}
	return JID{user: digits, server: serverUser}, nil
}

// MustJID returns Parse(input) and panics on error. It is intended for
// tests only — production code MUST use Parse and handle the error.
func MustJID(input string) JID {
	j, err := Parse(input)
	if err != nil {
		panic(err) //nolint:forbidigo // test-only helper, documented panic-on-error
	}
	return j
}

// String returns the canonical "<user>@<server>" form. The round-trip
// invariant Parse(j.String()) == j holds for every j produced by Parse,
// ParsePhone, or MustJID.
func (j JID) String() string {
	if j.IsZero() {
		return ""
	}
	return j.user + "@" + j.server
}

// User returns the user part (the digit string for user JIDs, the group
// ID for group JIDs).
func (j JID) User() string { return j.user }

// Server returns the server part ("s.whatsapp.net" or "g.us").
func (j JID) Server() string { return j.server }

// IsUser reports whether j is a personal user JID.
func (j JID) IsUser() bool { return j.server == serverUser }

// IsGroup reports whether j is a group JID.
func (j JID) IsGroup() bool { return j.server == serverGroup }

// IsZero reports whether j is the zero value.
func (j JID) IsZero() bool { return j.user == "" && j.server == "" }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func groupUserOK(s string) bool {
	if s == "" {
		return false
	}
	hasDigit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '-':
			// allowed separator
		default:
			return false
		}
	}
	return hasDigit
}
