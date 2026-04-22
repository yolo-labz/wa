package domain

import (
	"strings"
	"time"
)

// AuditAction is the audit-log enum, distinct from the policy Action
// enum. Every constant carries the Audit prefix per the data-model.md
// defensive-naming convention.
type AuditAction uint8

// AuditAction values. Zero is invalid.
const (
	AuditSend AuditAction = iota + 1
	AuditReceive
	AuditPair
	AuditGrant
	AuditRevoke
	AuditPanic
	AuditHistoryComplete
	AuditStreamDrop
	AuditMigration
	AuditIdempotencyCollision
	// Feature 018 Tier 2 additions. Stable integer values must not be
	// reordered: they are persisted in audit.log and read by forensic
	// tooling. Append new kinds at the end.
	AuditEdit
	AuditBlock
	AuditUnblock
	AuditLeaveGroup
	AuditPrivacyChange
	AuditLogoutAll
	AuditGroupCreate
	AuditGroupParticipantsPatch
	AuditGroupInviteJoin
	AuditDisappearingSet
	AuditProfileEdit
)

// auditActionNames is the canonical lowercase wire tag for each
// AuditAction constant. Indexed by integer value; position 0 is the
// invalid zero. Table form instead of a large switch keeps gocyclo
// bounded as new audit kinds are appended per feature.
var auditActionNames = [...]string{
	0:                                "unknown",
	int(AuditSend):                   "send",
	int(AuditReceive):                "receive",
	int(AuditPair):                   "pair",
	int(AuditGrant):                  "grant",
	int(AuditRevoke):                 "revoke",
	int(AuditPanic):                  "panic",
	int(AuditHistoryComplete):        "history_complete",
	int(AuditStreamDrop):             "stream_drop",
	int(AuditMigration):              "migration",
	int(AuditIdempotencyCollision):   "idempotency_collision",
	int(AuditEdit):                   "edit",
	int(AuditBlock):                  "block",
	int(AuditUnblock):                "unblock",
	int(AuditLeaveGroup):             "leave_group",
	int(AuditPrivacyChange):          "privacy_change",
	int(AuditLogoutAll):              "logout_all",
	int(AuditGroupCreate):            "group_create",
	int(AuditGroupParticipantsPatch): "group_participants_patch",
	int(AuditGroupInviteJoin):        "group_invite_join",
	int(AuditDisappearingSet):        "disappearing_set",
	int(AuditProfileEdit):            "profile_edit",
}

// String returns the canonical lowercase name of the audit action.
func (a AuditAction) String() string {
	if int(a) < len(auditActionNames) {
		if name := auditActionNames[a]; name != "" {
			return name
		}
	}
	return "unknown"
}

// AuditEvent is a single entry in the append-only audit log. It is a
// pure value and is stamped at construction time via NewAuditEvent — the
// only sanctioned use of time.Now() in internal/domain.
type AuditEvent struct {
	ID       EventID
	TS       time.Time
	Actor    string
	Action   AuditAction
	Subject  JID
	Decision string
	Detail   string
}

// NewAuditEvent constructs an AuditEvent stamped with time.Now().
func NewAuditEvent(actor string, action AuditAction, subject JID, decision string, detail string) AuditEvent {
	return AuditEvent{
		TS:       time.Now().UTC(),
		Actor:    actor,
		Action:   action,
		Subject:  subject,
		Decision: decision,
		Detail:   detail,
	}
}

// String returns a deterministic single-line JSON-ish representation
// suitable for log output. It is hand-rolled to avoid pulling
// encoding/json into the domain package.
func (e AuditEvent) String() string {
	var b strings.Builder
	b.WriteString(`{"id":`)
	writeJSONString(&b, e.ID.String())
	b.WriteString(`,"ts":`)
	writeJSONString(&b, e.TS.UTC().Format(time.RFC3339Nano))
	b.WriteString(`,"actor":`)
	writeJSONString(&b, e.Actor)
	b.WriteString(`,"action":`)
	writeJSONString(&b, e.Action.String())
	b.WriteString(`,"subject":`)
	writeJSONString(&b, e.Subject.String())
	b.WriteString(`,"decision":`)
	writeJSONString(&b, e.Decision)
	b.WriteString(`,"detail":`)
	writeJSONString(&b, e.Detail)
	b.WriteString(`}`)
	return b.String()
}

// hexDigits is the lowercase hex alphabet used for JSON \u00xx escapes.
const hexDigits = "0123456789abcdef"

func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hexDigits[c>>4])
				b.WriteByte(hexDigits[c&0xf])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}
