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
)

// String returns the canonical lowercase name of the audit action.
func (a AuditAction) String() string {
	switch a {
	case AuditSend:
		return "send"
	case AuditReceive:
		return "receive"
	case AuditPair:
		return "pair"
	case AuditGrant:
		return "grant"
	case AuditRevoke:
		return "revoke"
	case AuditPanic:
		return "panic"
	default:
		return "unknown"
	}
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
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0xf])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}
