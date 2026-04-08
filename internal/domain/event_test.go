package domain

import (
	"testing"
	"time"
)

func TestReceiptStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []ReceiptStatus{ReceiptDelivered, ReceiptRead, ReceiptPlayed} {
		if !s.IsValid() || s.String() == "unknown" {
			t.Errorf("%v should be valid", s)
		}
	}
	var zero ReceiptStatus
	if zero.IsValid() {
		t.Error("zero ReceiptStatus should be invalid")
	}
}

func TestConnectionState(t *testing.T) {
	t.Parallel()
	for _, c := range []ConnectionState{ConnDisconnected, ConnConnecting, ConnConnected} {
		if !c.IsValid() || c.String() == "unknown" {
			t.Errorf("%v invalid", c)
		}
	}
	var zero ConnectionState
	if zero.IsValid() {
		t.Error("zero invalid")
	}
}

func TestPairingState(t *testing.T) {
	t.Parallel()
	for _, p := range []PairingState{PairQRCode, PairPhoneCode, PairSuccess, PairFailure} {
		if !p.IsValid() || p.String() == "unknown" {
			t.Errorf("%v invalid", p)
		}
	}
}

func TestEvent_SealedInterface(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	evs := []Event{
		MessageEvent{ID: "e1", TS: now, From: testRecipient, Message: TextMessage{Recipient: testRecipient, Body: "x"}},
		ReceiptEvent{ID: "e2", TS: now, Chat: testRecipient, MessageID: "m1", Status: ReceiptDelivered},
		ConnectionEvent{ID: "e3", TS: now, State: ConnConnected},
		PairingEvent{ID: "e4", TS: now, State: PairSuccess},
	}
	for i, e := range evs {
		if e.EventID().IsZero() {
			t.Errorf("event[%d] has zero id", i)
		}
		if !e.Timestamp().Equal(now) {
			t.Errorf("event[%d] timestamp mismatch", i)
		}
	}
}
