package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// LabelPaletteSize is the number of WhatsApp Business label colours.
// whatsmeow's LabelEdit enforces 0..19 inclusive (FR-120).
const LabelPaletteSize = 20

// LabelNameMaxLen is the server-enforced max UTF-8 length for label names.
// WhatsApp Business truncates anything longer in the mobile UI.
const LabelNameMaxLen = 100

// ErrLabelNameEmpty is returned when a label is created or renamed with
// an empty name (after trimming).
var ErrLabelNameEmpty = errors.New("label: name required")

// ErrLabelNameTooLong is returned when a label name exceeds LabelNameMaxLen
// UTF-8 bytes.
var ErrLabelNameTooLong = errors.New("label: name too long")

// ErrLabelColorOutOfRange is returned when the colour index is not in
// [0, LabelPaletteSize).
var ErrLabelColorOutOfRange = errors.New("label: color index out of range")

// ErrLabelAssignmentTarget is returned when a LabelAssignment is built with
// an unsupported target kind (neither chat nor message).
var ErrLabelAssignmentTarget = errors.New("label: unsupported assignment target")

// LabelTargetKind enumerates what a LabelAssignment can hang off of. v0.5
// supports chat-level and message-level assignments, matching the
// whatsmeow Business surface.
type LabelTargetKind uint8

// LabelTargetKind values. Zero is invalid.
const (
	LabelTargetChat LabelTargetKind = iota + 1
	LabelTargetMessage
)

// IsValid reports whether k is one of the declared target kinds.
func (k LabelTargetKind) IsValid() bool {
	return k == LabelTargetChat || k == LabelTargetMessage
}

// String returns the canonical lowercase name used on the wire.
func (k LabelTargetKind) String() string {
	switch k {
	case LabelTargetChat:
		return "chat"
	case LabelTargetMessage:
		return "message"
	default:
		return "unknown"
	}
}

// Label is the FR-120 domain entity for a WhatsApp Business label. Labels
// are server-assigned-id opaque tags with a user-visible name and one of
// LabelPaletteSize colours. The type is immutable from outside; rename and
// recolour return new values.
type Label struct {
	id         string
	name       string
	colorIndex int
	createdAt  time.Time
	updatedAt  time.Time
}

// NewLabel constructs a Label after validating name length and colour. The
// name is trimmed of surrounding whitespace; the trimmed value must be
// non-empty and ≤ LabelNameMaxLen bytes.
func NewLabel(id, name string, colorIndex int, createdAt time.Time) (Label, error) {
	if id == "" {
		return Label{}, fmt.Errorf("label: id required")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Label{}, ErrLabelNameEmpty
	}
	if len(trimmed) > LabelNameMaxLen {
		return Label{}, ErrLabelNameTooLong
	}
	if colorIndex < 0 || colorIndex >= LabelPaletteSize {
		return Label{}, ErrLabelColorOutOfRange
	}
	return Label{
		id:         id,
		name:       trimmed,
		colorIndex: colorIndex,
		createdAt:  createdAt.UTC(),
		updatedAt:  createdAt.UTC(),
	}, nil
}

// ID returns the opaque server-assigned identifier.
func (l Label) ID() string { return l.id }

// Name returns the trimmed user-visible name.
func (l Label) Name() string { return l.name }

// ColorIndex returns the palette index in [0, LabelPaletteSize).
func (l Label) ColorIndex() int { return l.colorIndex }

// CreatedAt returns the creation timestamp in UTC.
func (l Label) CreatedAt() time.Time { return l.createdAt }

// UpdatedAt returns the timestamp of the last mutation in UTC.
func (l Label) UpdatedAt() time.Time { return l.updatedAt }

// Rename returns a copy of l with a new (trimmed, validated) name and an
// updated timestamp. The original is not mutated.
func (l Label) Rename(name string, at time.Time) (Label, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return l, ErrLabelNameEmpty
	}
	if len(trimmed) > LabelNameMaxLen {
		return l, ErrLabelNameTooLong
	}
	l.name = trimmed
	l.updatedAt = at.UTC()
	return l, nil
}

// Recolor returns a copy of l with a new palette index and an updated
// timestamp.
func (l Label) Recolor(colorIndex int, at time.Time) (Label, error) {
	if colorIndex < 0 || colorIndex >= LabelPaletteSize {
		return l, ErrLabelColorOutOfRange
	}
	l.colorIndex = colorIndex
	l.updatedAt = at.UTC()
	return l, nil
}

// HydrateLabel reconstructs a Label from previously-stored fields without
// rerunning validation. Persistence layers use this when rehydrating from
// SQLite; callers MUST NOT use it to bypass invariants on user input.
func HydrateLabel(id, name string, colorIndex int, createdAt, updatedAt time.Time) Label {
	return Label{
		id:         id,
		name:       name,
		colorIndex: colorIndex,
		createdAt:  createdAt.UTC(),
		updatedAt:  updatedAt.UTC(),
	}
}

// LabelAssignment is the FR-120 domain link between a Label and a target
// (chat JID or message id). The (labelID, targetKind, target) triple is
// unique — the same label can be attached to many chats/messages, but a
// given (chat, label) or (message, label) pair exists at most once.
type LabelAssignment struct {
	labelID    string
	targetKind LabelTargetKind
	chat       JID       // populated when targetKind == LabelTargetChat; also populated for message assignments (the chat containing the message)
	messageID  MessageID // populated when targetKind == LabelTargetMessage
	createdAt  time.Time
}

// NewChatLabelAssignment constructs an assignment of a label to a chat.
// chat must be non-zero; labelID must be non-empty.
func NewChatLabelAssignment(labelID string, chat JID, at time.Time) (LabelAssignment, error) {
	if labelID == "" {
		return LabelAssignment{}, fmt.Errorf("label_assignment: labelID required")
	}
	if chat.IsZero() {
		return LabelAssignment{}, fmt.Errorf("label_assignment: chat required")
	}
	return LabelAssignment{
		labelID:    labelID,
		targetKind: LabelTargetChat,
		chat:       chat,
		createdAt:  at.UTC(),
	}, nil
}

// NewMessageLabelAssignment constructs an assignment of a label to a
// specific message inside chat. Both chat and messageID are required so
// that removal does not require a separate chat lookup.
func NewMessageLabelAssignment(labelID string, chat JID, messageID MessageID, at time.Time) (LabelAssignment, error) {
	if labelID == "" {
		return LabelAssignment{}, fmt.Errorf("label_assignment: labelID required")
	}
	if chat.IsZero() {
		return LabelAssignment{}, fmt.Errorf("label_assignment: chat required")
	}
	if messageID == "" {
		return LabelAssignment{}, fmt.Errorf("label_assignment: messageID required")
	}
	return LabelAssignment{
		labelID:    labelID,
		targetKind: LabelTargetMessage,
		chat:       chat,
		messageID:  messageID,
		createdAt:  at.UTC(),
	}, nil
}

// LabelID returns the referenced label's opaque id.
func (a LabelAssignment) LabelID() string { return a.labelID }

// TargetKind returns the assignment kind (chat or message).
func (a LabelAssignment) TargetKind() LabelTargetKind { return a.targetKind }

// Chat returns the chat JID (always populated; for message assignments it
// is the containing chat).
func (a LabelAssignment) Chat() JID { return a.chat }

// MessageID returns the message id for message assignments, or "" for
// chat assignments.
func (a LabelAssignment) MessageID() MessageID { return a.messageID }

// CreatedAt returns the assignment creation timestamp in UTC.
func (a LabelAssignment) CreatedAt() time.Time { return a.createdAt }
