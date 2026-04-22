package domain

import "fmt"

// AvatarSize enumerates the two profile-photo resolutions WhatsApp
// exposes through contacts.profilePhoto: the small "preview" crop used in
// chat lists and the full-resolution image used on the profile screen.
// The wire form is the lowercase String() tag.
type AvatarSize uint8

// AvatarSize values. Zero is invalid.
const (
	AvatarPreview AvatarSize = iota + 1
	AvatarFull
)

// String returns the canonical lowercase wire tag.
func (s AvatarSize) String() string {
	switch s {
	case AvatarPreview:
		return "preview"
	case AvatarFull:
		return "full"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the two declared sizes.
func (s AvatarSize) IsValid() bool { return s == AvatarPreview || s == AvatarFull }

// ParseAvatarSize maps a wire token to the enum. An empty string defaults
// to AvatarPreview per the socket layer's documented default.
func ParseAvatarSize(s string) (AvatarSize, error) {
	switch s {
	case "", "preview":
		return AvatarPreview, nil
	case "full":
		return AvatarFull, nil
	default:
		return 0, fmt.Errorf("domain: invalid avatar size %q (want preview|full)", s)
	}
}
