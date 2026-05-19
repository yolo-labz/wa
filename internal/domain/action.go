package domain

import "fmt"

// Action is the policy enum for the allowlist. It is uint8 with iota+1
// values so the zero value is invalid by construction.
//
// Feature 018 FR-050 extended the enum from 4 members (read/send/group.add/
// group.create) to cover every Tier-2 mutating method. The existing four
// keep their ordinal positions so historical audit rows and TOML allowlist
// files written before v2.0.0-rc2 round-trip unchanged.
type Action uint8

// Action values. The zero value is intentionally invalid.
const (
	ActionRead Action = iota + 1
	ActionSend
	ActionGroupAdd
	ActionGroupCreate
	// Feature 018 T2-24: one action per Tier-2 mutating surface so the
	// allowlist middleware can deny at method granularity.
	ActionRevoke
	ActionEdit
	ActionBlock
	ActionGroupRemove
	ActionGroupPromote
	ActionGroupDemote
	ActionGroupLeave
	ActionGroupEdit
	ActionGroupInvite
	ActionPrivacySet
	ActionProfileEdit
	ActionLogoutAll
	ActionPresence
	ActionChatState
	// ActionLogout — single-device server-side unlink (this client only).
	// Feature 110f / `wa pair --reset`. Distinct from ActionLogoutAll
	// because this does not affect other linked devices on the account.
	ActionLogout
)

// actionNames is the canonical String() / ParseAction() mapping. Single
// source of truth so new actions only need one edit.
var actionNames = [...]string{
	ActionRead:         "read",
	ActionSend:         "send",
	ActionGroupAdd:     "group.add",
	ActionGroupCreate:  "group.create",
	ActionRevoke:       "revoke",
	ActionEdit:         "edit",
	ActionBlock:        "block",
	ActionGroupRemove:  "group.remove",
	ActionGroupPromote: "group.promote",
	ActionGroupDemote:  "group.demote",
	ActionGroupLeave:   "group.leave",
	ActionGroupEdit:    "group.edit",
	ActionGroupInvite:  "group.invite",
	ActionPrivacySet:   "privacy.set",
	ActionProfileEdit:  "profile.edit",
	ActionLogoutAll:    "logout.all",
	ActionPresence:     "presence",
	ActionChatState:    "chat.state",
	ActionLogout:       "logout",
}

// String returns the canonical lowercase name of the action.
func (a Action) String() string {
	if int(a) < len(actionNames) && actionNames[a] != "" {
		return actionNames[a]
	}
	return "unknown"
}

// IsValid reports whether a is one of the declared Action constants.
func (a Action) IsValid() bool {
	return int(a) < len(actionNames) && actionNames[a] != ""
}

// ParseAction returns the Action whose String() form equals s, or
// ErrUnknownAction wrapped with the input.
func ParseAction(s string) (Action, error) {
	for i, n := range actionNames {
		if n == "" {
			continue
		}
		if n == s {
			return Action(i), nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownAction, s)
}

// AllActions returns a defensive copy of every declared Action in ordinal
// order. Used by coverage tests to assert that every mutating method has a
// named Action (FR-050).
func AllActions() []Action {
	out := make([]Action, 0, len(actionNames)-1)
	for i, n := range actionNames {
		if i == 0 || n == "" {
			continue
		}
		out = append(out, Action(i))
	}
	return out
}
