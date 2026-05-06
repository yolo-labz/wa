package domain

import (
	"fmt"
	"log/slog"
	"strings"
)

const (
	serverUser  = "s.whatsapp.net"
	serverGroup = "g.us"
	// serverLID is whatsmeow's HiddenUserServer ("lid"). WhatsApp returns
	// LIDs in place of phone-number JIDs whenever the contact's PN was
	// never disclosed to this account: business-discovery flows,
	// LinkedIn-click-to-WA deep links, group joins by invite, and as the
	// new default identity for fresh sessions in Multi-Device 2024+.
	// The LID user part is a numeric string identical in shape to a PN
	// but lives in a separate namespace — `<digits>@lid` is NOT
	// interchangeable with `<digits>@s.whatsapp.net`. Resolution between
	// the two namespaces is a separate concern (whatsmeow's
	// `Client.Store.LIDs`) and is not performed by this domain type.
	serverLID = "lid"

	// serverHostedLID is whatsmeow's HostedLIDServer ("hosted.lid"), the
	// LID variant assigned to enterprise-hosted accounts (WhatsApp
	// Business Cloud API tenants). Same numeric user part shape as a
	// regular LID but a separate namespace from `lid` — neither
	// interchangeable. Spec 108.
	serverHostedLID = "hosted.lid"

	// serverHosted is whatsmeow's HostedServer ("hosted"), the hosted
	// counterpart of `s.whatsapp.net` for enterprise tenants. Spec 108.
	serverHosted = "hosted"

	// serverBot is whatsmeow's BotServer ("bot"). Used by Meta AI and
	// other first-party bots WhatsApp surfaces in user threads. Bots
	// are ADDRESSABLE for messaging (the user's primary interaction
	// surface) but are NOT eligible for allowlist `read` /
	// `group.add` actions — bot conversations are 1:1 by construction.
	// Spec 108.
	serverBot = "bot"

	// serverBroadcast is whatsmeow's BroadcastServer ("broadcast").
	// CLAUDE.md §Safety enshrines "no broadcast lists ever" as a
	// hard refusal — broadcast lists are a high-ban-risk pattern
	// WhatsApp's anti-spam heuristics flag aggressively. The parser
	// recognises the namespace specifically to return a typed
	// `ErrBroadcastForbidden` instead of the generic "unknown server"
	// error so callers see actionable feedback. Spec 108.
	serverBroadcast = "broadcast"

	// serverNewsletter is whatsmeow's NewsletterServer ("newsletter"),
	// WhatsApp Channels. Newsletters have a one-to-many fan-out
	// (admin posts → followers receive) and are NOT addressable for
	// direct send — only group-style `groups.get` style metadata reads
	// and (eventually) admin-only `channel.publish` actions. Spec 108
	// recognises the namespace + classifies it as IsChannel; admin /
	// publish surface is deferred to a future feature.
	serverNewsletter = "newsletter"

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
// user JID ("5511...@s.whatsapp.net"), a canonical LID ("66448...@lid"),
// or a canonical group JID ("120363...@g.us"), and returns the
// corresponding JID.
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
	case serverUser, serverLID, serverHosted, serverHostedLID, serverBot:
		// All five addressable namespaces use a numeric user part.
		// They are distinct identities — `<digits>@lid` is NOT
		// interchangeable with `<digits>@hosted.lid` etc. See per-
		// namespace doc on each constant for usage notes.
		if !allDigits(user) {
			return JID{}, fmt.Errorf("%w: non-digit user in %q", ErrInvalidJID, input)
		}
	case serverGroup, serverNewsletter:
		// Newsletters use the same numeric+hyphen user-part shape as
		// groups (whatsmeow `types/jid.go` validates them with the
		// same regex). Validate via groupUserOK.
		if !groupUserOK(user) {
			return JID{}, fmt.Errorf("%w: invalid %s user in %q", ErrInvalidJID, server, input)
		}
	case serverBroadcast:
		// CLAUDE.md §Safety: "no broadcast lists ever". Refuse on
		// sight with a typed sentinel so callers (CLI, allowlist
		// loader) can branch deliberately instead of silently
		// blanket-denying with a generic error.
		return JID{}, fmt.Errorf("%w: broadcast lists are forbidden by safety policy", ErrBroadcastForbidden)
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

// LogValue implements slog.LogValuer so a JID logged via slog
// renders as its canonical string form rather than the default
// reflect-based struct dump (which would expose the unexported
// fields). Without this, `log.Info("msg", "jid", j)` would emit
// `jid={user:5511...,server:s.whatsapp.net}` — noisy and harder
// to grep. Spec 016 FR-013 / T067.
func (j JID) LogValue() slog.Value { return slog.StringValue(j.String()) }

// User returns the user part (the digit string for user JIDs, the group
// ID for group JIDs).
func (j JID) User() string { return j.user }

// Server returns the server part ("s.whatsapp.net", "lid", or "g.us").
func (j JID) Server() string { return j.server }

// IsUser reports whether j is a personal phone-number user JID
// ("@s.whatsapp.net"). Returns false for LIDs — use IsLID or IsAddressable
// when both addressable forms should match.
func (j JID) IsUser() bool { return j.server == serverUser }

// IsLID reports whether j is a LID ("@lid"). LIDs are first-class
// addressable identities; the WhatsApp protocol accepts them as the
// recipient of SendMessage just like phone-number JIDs.
func (j JID) IsLID() bool { return j.server == serverLID }

// IsHosted reports whether j is a hosted phone-number user JID
// ("@hosted") or hosted LID ("@hosted.lid"). Hosted accounts are the
// enterprise-tenant variants of `s.whatsapp.net` and `lid`; the
// daemon treats them as addressable but logs them distinctly so audit
// trails preserve the tenant boundary. Spec 108.
func (j JID) IsHosted() bool {
	return j.server == serverHosted || j.server == serverHostedLID
}

// IsBot reports whether j is a WhatsApp bot ("@bot") — Meta AI and
// other first-party bots. Spec 108.
func (j JID) IsBot() bool { return j.server == serverBot }

// IsChannel reports whether j is a WhatsApp Channel ("@newsletter").
// Channels are read-mostly: followers receive admin posts via fan-out
// and the daemon supports metadata reads but not direct addressed
// send. Spec 108.
func (j JID) IsChannel() bool { return j.server == serverNewsletter }

// IsAddressable reports whether j is a JID that can receive a 1:1
// message directly — phone-number user JID, LID, hosted PN, hosted
// LID, or bot. Excludes groups (use GroupManager) and channels
// (broadcast-style, no per-recipient address). Spec 108 expanded
// from {user, LID} to the full addressable set.
func (j JID) IsAddressable() bool {
	switch j.server {
	case serverUser, serverLID, serverHosted, serverHostedLID, serverBot:
		return true
	default:
		return false
	}
}

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
