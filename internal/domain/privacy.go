package domain

import "fmt"

// PrivacyKey enumerates the WhatsApp privacy-setting dimensions exposed
// through privacy.set / privacy.get. The wire form is the lowercase
// String() tag.
type PrivacyKey uint8

// PrivacyKey values. Zero is invalid.
const (
	PrivacyKeyGroups PrivacyKey = iota + 1
	PrivacyKeyReadReceipts
	PrivacyKeyLastSeen
	PrivacyKeyProfile
	PrivacyKeyAbout
)

// String returns the canonical lowercase wire tag.
func (k PrivacyKey) String() string {
	switch k {
	case PrivacyKeyGroups:
		return "groups"
	case PrivacyKeyReadReceipts:
		return "readReceipts"
	case PrivacyKeyLastSeen:
		return "lastSeen"
	case PrivacyKeyProfile:
		return "profile"
	case PrivacyKeyAbout:
		return "about"
	default:
		return "unknown"
	}
}

// IsValid reports whether k is one of the five declared keys.
func (k PrivacyKey) IsValid() bool {
	switch k {
	case PrivacyKeyGroups, PrivacyKeyReadReceipts, PrivacyKeyLastSeen, PrivacyKeyProfile, PrivacyKeyAbout:
		return true
	default:
		return false
	}
}

// ParsePrivacyKey maps a wire token to the enum.
func ParsePrivacyKey(s string) (PrivacyKey, error) {
	switch s {
	case "groups":
		return PrivacyKeyGroups, nil
	case "readReceipts":
		return PrivacyKeyReadReceipts, nil
	case "lastSeen":
		return PrivacyKeyLastSeen, nil
	case "profile":
		return PrivacyKeyProfile, nil
	case "about":
		return PrivacyKeyAbout, nil
	default:
		return 0, fmt.Errorf("domain: invalid privacy key %q (want groups|readReceipts|lastSeen|profile|about)", s)
	}
}

// PrivacyValue enumerates the audience tiers a PrivacyKey can be set to.
// The wire form is the lowercase String() tag.
type PrivacyValue uint8

// PrivacyValue values. Zero is invalid.
const (
	PrivacyValueEveryone PrivacyValue = iota + 1
	PrivacyValueContacts
	PrivacyValueNobody
)

// String returns the canonical lowercase wire tag.
func (v PrivacyValue) String() string {
	switch v {
	case PrivacyValueEveryone:
		return "everyone"
	case PrivacyValueContacts:
		return "contacts"
	case PrivacyValueNobody:
		return "nobody"
	default:
		return "unknown"
	}
}

// IsValid reports whether v is one of the three declared values.
func (v PrivacyValue) IsValid() bool {
	switch v {
	case PrivacyValueEveryone, PrivacyValueContacts, PrivacyValueNobody:
		return true
	default:
		return false
	}
}

// ParsePrivacyValue maps a wire token to the enum.
func ParsePrivacyValue(s string) (PrivacyValue, error) {
	switch s {
	case "everyone":
		return PrivacyValueEveryone, nil
	case "contacts":
		return PrivacyValueContacts, nil
	case "nobody":
		return PrivacyValueNobody, nil
	default:
		return 0, fmt.Errorf("domain: invalid privacy value %q (want everyone|contacts|nobody)", s)
	}
}

// PrivacyTuple pairs a PrivacyKey with a PrivacyValue. It is the atomic
// unit of a privacy.set call.
type PrivacyTuple struct {
	Key   PrivacyKey
	Value PrivacyValue
}

// Validate enforces that both Key and Value are declared enum members.
func (t PrivacyTuple) Validate() error {
	if !t.Key.IsValid() {
		return fmt.Errorf("domain: privacy tuple has invalid key %d", uint8(t.Key))
	}
	if !t.Value.IsValid() {
		return fmt.Errorf("domain: privacy tuple has invalid value %d", uint8(t.Value))
	}
	return nil
}
