package domain

import (
	"errors"
	"strings"
	"testing"
)

var testRecipient = MustJID("5511999999999")

func TestTextMessage_Validate(t *testing.T) {
	t.Parallel()
	if err := (TextMessage{Recipient: testRecipient, Body: "hi"}).Validate(); err != nil {
		t.Errorf("happy: %v", err)
	}
	if err := (TextMessage{Body: "hi"}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero recipient: %v", err)
	}
	if err := (TextMessage{Recipient: testRecipient, Body: ""}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty body: %v", err)
	}
	big := strings.Repeat("x", MaxTextBytes+1)
	if err := (TextMessage{Recipient: testRecipient, Body: big}).Validate(); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("oversized: %v", err)
	}
}

func TestMediaMessage_Validate(t *testing.T) {
	t.Parallel()
	if err := (MediaMessage{Recipient: testRecipient, Path: "/x", Mime: "image/png"}).Validate(); err != nil {
		t.Errorf("happy: %v", err)
	}
	if err := (MediaMessage{Path: "/x", Mime: "image/png"}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero recipient: %v", err)
	}
	if err := (MediaMessage{Recipient: testRecipient, Mime: "image/png"}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty path: %v", err)
	}
	if err := (MediaMessage{Recipient: testRecipient, Path: "/x"}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty mime: %v", err)
	}
}

func TestReactionMessage_Validate(t *testing.T) {
	t.Parallel()
	if err := (ReactionMessage{Recipient: testRecipient, TargetID: MessageID("m1"), Emoji: "👍"}).Validate(); err != nil {
		t.Errorf("happy: %v", err)
	}
	// empty emoji allowed = remove reaction
	if err := (ReactionMessage{Recipient: testRecipient, TargetID: MessageID("m1")}).Validate(); err != nil {
		t.Errorf("empty emoji should be allowed: %v", err)
	}
	if err := (ReactionMessage{TargetID: MessageID("m1")}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero recipient: %v", err)
	}
	if err := (ReactionMessage{Recipient: testRecipient}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("zero target: %v", err)
	}
}

func TestMessage_SealedInterface(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		TextMessage{Recipient: testRecipient, Body: "hi"},
		MediaMessage{Recipient: testRecipient, Path: "/x", Mime: "image/png"},
		ReactionMessage{Recipient: testRecipient, TargetID: MessageID("m1")},
	}
	for _, m := range msgs {
		if m.To() != testRecipient {
			t.Errorf("To() mismatch")
		}
	}
}
