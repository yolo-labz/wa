package domain

import (
	"errors"
	"time"
)

// EditWindow is the spec-mandated window within which a previously-sent
// message may still be edited (FR-015a). whatsmeow's upstream constant is
// 20 minutes but the wa policy tightens this to 15 to leave a safety
// margin against clock skew between client and server. The adapter MUST
// refuse edits whose original timestamp is older than now-EditWindow with
// ErrOutsideEditWindow, which the socket dispatcher routes to
// -32100 policy_refused.
const EditWindow = 15 * time.Minute

// ErrOutsideEditWindow is returned by MessageModerator.Edit when the
// target message's timestamp is older than now-EditWindow. Wrapped via
// fmt.Errorf("%w: ...", ErrOutsideEditWindow, ...) so callers can
// errors.Is for the category. Maps to -32100 policy_refused at the
// socket boundary.
var ErrOutsideEditWindow = errors.New("domain: edit outside 15-min window")
