package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ContactRefKind enumerates the three shapes a ContactRef may take.
type ContactRefKind uint8

// ContactRefKind values. Zero is invalid.
const (
	ContactRefJID ContactRefKind = iota + 1
	ContactRefPhone
	ContactRefName
)

// String returns the canonical name of the contact-ref kind.
func (k ContactRefKind) String() string {
	switch k {
	case ContactRefJID:
		return "jid"
	case ContactRefPhone:
		return "phone"
	case ContactRefName:
		return "name"
	default:
		return "unknown"
	}
}

// ErrContactRefShape indicates a ContactRef populated with zero or more than
// one shape. Per data-model.md §ContactRef, exactly one of the three fields
// MUST be set by the caller.
var ErrContactRefShape = errors.New("domain: ContactRef must populate exactly one of jid|phone|name")

// ContactRef is the union accepted on every socket method or MCP tool that
// addresses a contact. Exactly one of the three shapes is populated per
// value; this is an invariant enforced by Validate.
//
// The domain performs only shape validation — canonical-phone normalisation
// (E.164 regional parsing via nyaruka/phonenumbers) and directory resolution
// of name matches live in the ContactDirectory port implementation.
type ContactRef struct {
	JID   JID    // kind == ContactRefJID when !JID.IsZero()
	Phone string // kind == ContactRefPhone when non-empty
	Name  string // kind == ContactRefName when non-empty
}

// NewJIDRef constructs a ContactRef populated with a JID.
func NewJIDRef(j JID) ContactRef { return ContactRef{JID: j} }

// NewPhoneRef constructs a ContactRef populated with a phone string (in
// E.164-ish form, validated at the adapter boundary).
func NewPhoneRef(phone string) ContactRef { return ContactRef{Phone: phone} }

// NewNameRef constructs a ContactRef populated with a name substring.
func NewNameRef(name string) ContactRef { return ContactRef{Name: name} }

// Kind returns which of the three shapes is populated. It returns 0 if
// Validate would fail; callers that care about validity MUST call Validate
// explicitly.
func (r ContactRef) Kind() ContactRefKind {
	set := 0
	var kind ContactRefKind
	if !r.JID.IsZero() {
		set++
		kind = ContactRefJID
	}
	if strings.TrimSpace(r.Phone) != "" {
		set++
		kind = ContactRefPhone
	}
	if strings.TrimSpace(r.Name) != "" {
		set++
		kind = ContactRefName
	}
	if set != 1 {
		return 0
	}
	return kind
}

// Validate enforces the "exactly one shape populated" invariant and
// performs the minimum-viable domain-level shape checks on each variant.
// Deeper validation (E.164 region parsing, substring-length policy) lives
// in the ContactDirectory adapter and its tests.
func (r ContactRef) Validate() error {
	switch r.Kind() {
	case ContactRefJID:
		if r.JID.IsZero() {
			return fmt.Errorf("%w: JID is zero", ErrContactRefShape)
		}
		return nil
	case ContactRefPhone:
		p := strings.TrimSpace(r.Phone)
		if p == "" {
			return fmt.Errorf("%w: phone is empty", ErrContactRefShape)
		}
		digits := strings.TrimPrefix(p, "+")
		if len(digits) < minPhoneDigits || len(digits) > maxPhoneDigits {
			return fmt.Errorf("%w: phone %q out of E.164 range [%d,%d]", ErrInvalidPhone, r.Phone, minPhoneDigits, maxPhoneDigits)
		}
		for _, c := range digits {
			if c < '0' || c > '9' {
				return fmt.Errorf("%w: phone %q contains non-digit %q", ErrInvalidPhone, r.Phone, c)
			}
		}
		return nil
	case ContactRefName:
		n := strings.TrimSpace(r.Name)
		if n == "" {
			return fmt.Errorf("%w: name is empty", ErrContactRefShape)
		}
		return nil
	default:
		return ErrContactRefShape
	}
}
