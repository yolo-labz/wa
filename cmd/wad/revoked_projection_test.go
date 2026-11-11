package main

// A revoked message must not project its text to any consumer.
//
// 02/11/2026: both revokes were accepted (audit `action=revoke decision=ok`,
// row stamped in messages.db) and `wa history` still printed the full original
// text, because the projection never looked at revoked_at. That single defect
// made a successful revoke indistinguishable from a failed one for the person
// verifying it — and would have kept leaking dead text into every export.

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

func TestStoredToWireDropsTheTextOfARevokedMessage(t *testing.T) {
	wire := storedToWire([]sqlitehistory.StoredMessage{
		{MessageID: "LIVE", ChatJID: "c@lid", Body: "texto vivo", IsFromMe: true, Timestamp: 10},
		{
			MessageID: "DEAD", ChatJID: "c@lid", Body: "texto apagado", IsFromMe: true,
			Timestamp: 11, RevokedAt: 1793645274,
		},
	})
	if len(wire) != 2 {
		t.Fatalf("wire rows = %d, want 2", len(wire))
	}
	if wire[0].Body != "texto vivo" {
		t.Errorf("live body = %q, want it unchanged", wire[0].Body)
	}
	dead := wire[1]
	if !dead.Revoked {
		t.Error("revoked row is not marked Revoked")
	}
	if dead.RevokedAt != 1793645274 {
		t.Errorf("RevokedAt = %d, want 1793645274", dead.RevokedAt)
	}
	if dead.Body != "" || dead.Caption != "" {
		t.Errorf("revoked row leaked text: body=%q caption=%q", dead.Body, dead.Caption)
	}
	if dead.MessageID != "DEAD" || dead.Timestamp != 11 {
		t.Errorf("identity lost: %+v", dead)
	}
}

func TestStoredToWireDoesNotWrapTheTextOfARevokedInbound(t *testing.T) {
	wire := storedToWire([]sqlitehistory.StoredMessage{{
		MessageID: "IN-DEAD", ChatJID: "c@lid", SenderJID: "them@lid",
		Body: "inbound text that was revoked", IsFromMe: false, Timestamp: 12,
		RevokedAt: 1793645283,
	}})
	if len(wire) != 1 {
		t.Fatalf("wire rows = %d, want 1", len(wire))
	}
	if !wire[0].Revoked {
		t.Error("revoked inbound row is not marked Revoked")
	}
	if wire[0].Channel != "" {
		t.Errorf("revoked inbound row still carries a <channel> envelope: %q", wire[0].Channel)
	}
	if wire[0].Body != "" {
		t.Errorf("revoked inbound row carries body %q", wire[0].Body)
	}
}

// TestStoredToWireDropsTheTextOfADeleteForMe: a scope=self revoke stamps
// revoked_at negative; the operator deleted that text on purpose, so it
// reads as revoked like any other.
func TestStoredToWireDropsTheTextOfADeleteForMe(t *testing.T) {
	wire := storedToWire([]sqlitehistory.StoredMessage{{
		MessageID: "SELF", ChatJID: "c@lid", Body: "apagado para mim", Caption: "legenda", IsFromMe: true, RevokedAt: -1793645274,
	}})
	if w := wire[0]; !w.Revoked || w.Body != "" || w.Caption != "" {
		t.Errorf("delete-for-me row: revoked=%v body=%q caption=%q", w.Revoked, w.Body, w.Caption)
	}
}
