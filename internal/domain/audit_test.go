package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewAuditEvent_StampsTime(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC().Add(-time.Second)
	e := NewAuditEvent("wad", AuditSend, testRecipient, "allow", "")
	after := time.Now().UTC().Add(time.Second)
	if e.TS.Before(before) || e.TS.After(after) {
		t.Errorf("TS %v not in [%v,%v]", e.TS, before, after)
	}
}

func TestAuditEvent_String_SingleLine(t *testing.T) {
	t.Parallel()
	e := NewAuditEvent("wad", AuditSend, testRecipient, "allow", "ok")
	s := e.String()
	if strings.Contains(s, "\n") {
		t.Error("audit String must not contain newlines")
	}
	if !strings.HasPrefix(s, `{`) || !strings.HasSuffix(s, `}`) {
		t.Errorf("not JSON-ish: %q", s)
	}
	if !strings.Contains(s, `"action":"send"`) {
		t.Errorf("missing action: %q", s)
	}
	if !strings.Contains(s, `"actor":"wad"`) {
		t.Errorf("missing actor: %q", s)
	}
}

func TestAuditAction_Distinct(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, a := range []AuditAction{AuditSend, AuditReceive, AuditPair, AuditGrant, AuditRevoke, AuditPanic} {
		s := a.String()
		if seen[s] {
			t.Errorf("duplicate: %q", s)
		}
		seen[s] = true
	}
	if len(seen) != 6 {
		t.Errorf("want 6 distinct, got %d", len(seen))
	}
}

func TestAuditEvent_StringDeterministic(t *testing.T) {
	t.Parallel()
	e := AuditEvent{
		ID:       EventID("e1"),
		TS:       time.Unix(1_700_000_000, 0).UTC(),
		Actor:    "wad",
		Action:   AuditSend,
		Subject:  testRecipient,
		Decision: "allow",
		Detail:   "",
	}
	first := e.String()
	second := e.String()
	if first != second {
		t.Error("non-deterministic String")
	}
}
