package domain

import "fmt"

// Action is the policy enum for the allowlist. It is uint8 with iota+1
// values so the zero value is invalid by construction.
type Action uint8

// Action values. The zero value is intentionally invalid.
const (
	ActionRead Action = iota + 1
	ActionSend
	ActionGroupAdd
	ActionGroupCreate
)

// String returns the canonical lowercase name of the action.
func (a Action) String() string {
	switch a {
	case ActionRead:
		return "read"
	case ActionSend:
		return "send"
	case ActionGroupAdd:
		return "group.add"
	case ActionGroupCreate:
		return "group.create"
	default:
		return "unknown"
	}
}

// IsValid reports whether a is one of the four declared Action constants.
func (a Action) IsValid() bool {
	switch a {
	case ActionRead, ActionSend, ActionGroupAdd, ActionGroupCreate:
		return true
	default:
		return false
	}
}

// ParseAction returns the Action whose String() form equals s, or
// ErrUnknownAction wrapped with the input.
func ParseAction(s string) (Action, error) {
	switch s {
	case "read":
		return ActionRead, nil
	case "send":
		return ActionSend, nil
	case "group.add":
		return ActionGroupAdd, nil
	case "group.create":
		return ActionGroupCreate, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownAction, s)
	}
}
