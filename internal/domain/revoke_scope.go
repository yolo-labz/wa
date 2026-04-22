package domain

import "fmt"

// RevokeScope enumerates the two revoke modes exposed through
// message.revoke. "self" removes the message locally (deletes the
// local row, does not emit a tombstone to peers); "everyone" sends a
// tombstone to the chat so peers delete their copies.
type RevokeScope uint8

// RevokeScope values. Zero is invalid.
const (
	RevokeSelf RevokeScope = iota + 1
	RevokeEveryone
)

// String returns the canonical lowercase name used on the wire.
func (s RevokeScope) String() string {
	switch s {
	case RevokeSelf:
		return "self"
	case RevokeEveryone:
		return "everyone"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the two declared scopes.
func (s RevokeScope) IsValid() bool { return s == RevokeSelf || s == RevokeEveryone }

// ParseRevokeScope maps the wire token to the enum. Only lowercase
// "self" and "everyone" are accepted; any other input (including case
// variants, leading/trailing whitespace, or the empty string) yields
// an error so callers fail loudly rather than defaulting silently.
func ParseRevokeScope(s string) (RevokeScope, error) {
	switch s {
	case "self":
		return RevokeSelf, nil
	case "everyone":
		return RevokeEveryone, nil
	default:
		return 0, fmt.Errorf("domain: invalid revoke scope %q (want self|everyone)", s)
	}
}
