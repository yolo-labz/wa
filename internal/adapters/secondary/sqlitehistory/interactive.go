package sqlitehistory

import (
	"encoding/json"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// interactiveJSON is the on-disk / wire encoding of a
// domain.InteractivePayload stored in the messages.interactive_json BLOB
// column (issue #201). It is a stable, self-describing shape — Subtype is
// the lowercase string ("list"/"buttons") rather than the domain enum's
// numeric value, so a row inspected with `sqlite3` is human-readable and a
// future enum reorder cannot silently re-map persisted rows. It mirrors the
// shape the golden render test already documents.
type interactiveJSON struct {
	Subtype string                  `json:"subtype"`
	Prompt  string                  `json:"prompt"`
	Options []interactiveJSONOption `json:"options"`
}

type interactiveJSONOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// MarshalInteractive encodes a domain.InteractivePayload to the stable
// interactive_json BLOB encoding (issue #201). A nil payload returns
// (nil, nil) so callers persist SQL NULL for ordinary messages.
func MarshalInteractive(p *domain.InteractivePayload) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	dto := interactiveJSON{
		Subtype: p.Subtype.String(),
		Prompt:  p.Prompt,
		Options: make([]interactiveJSONOption, 0, len(p.Options)),
	}
	for _, o := range p.Options {
		dto.Options = append(dto.Options, interactiveJSONOption{ID: o.ID, Label: o.Label})
	}
	return json.Marshal(dto)
}

// UnmarshalInteractive decodes the interactive_json BLOB back into a
// domain.InteractivePayload (issue #201). Empty input returns (nil, nil)
// — the common case for ordinary messages. The string Subtype is mapped
// back to the domain enum; an unknown/legacy value yields the zero
// (invalid) subtype rather than an error so a single malformed row never
// breaks an entire history page.
func UnmarshalInteractive(b []byte) (*domain.InteractivePayload, error) {
	if len(b) == 0 {
		// Ordinary (non-interactive) message: nil payload, no error. This
		// is the documented "absence is not failure" contract callers rely
		// on (mirrors MarshalInteractive(nil) → (nil, nil)).
		return nil, nil //nolint:nilnil // absent interactive_json is a value, not an error
	}
	var dto interactiveJSON
	if err := json.Unmarshal(b, &dto); err != nil {
		return nil, err
	}
	p := &domain.InteractivePayload{
		Subtype: interactiveSubtypeFromString(dto.Subtype),
		Prompt:  dto.Prompt,
		Options: make([]domain.InteractiveOption, 0, len(dto.Options)),
	}
	for _, o := range dto.Options {
		p.Options = append(p.Options, domain.InteractiveOption{ID: o.ID, Label: o.Label})
	}
	return p, nil
}

// interactiveSubtypeFromString is the inverse of
// domain.InteractiveSubtype.String. Unknown input maps to the zero value
// (invalid subtype) — see UnmarshalInteractive for why this is non-fatal.
func interactiveSubtypeFromString(s string) domain.InteractiveSubtype {
	switch s {
	case "list":
		return domain.InteractiveList
	case "buttons":
		return domain.InteractiveButtons
	default:
		return 0
	}
}
