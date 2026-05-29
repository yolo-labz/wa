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

// TestMediaMessage_SourceValidate covers the spec-197 payload-source seam:
// EXACTLY ONE of Path / Bytes / SHA256, and inline Bytes ≤ MaxMediaBytes.
func TestMediaMessage_SourceValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		m    MediaMessage
		want error // nil = expect success
	}{
		{"path only", MediaMessage{Path: "/x"}, nil},
		{"bytes only", MediaMessage{Bytes: []byte("hello")}, nil},
		{"sha256 only", MediaMessage{SHA256: "abc123"}, nil},
		{"no source", MediaMessage{}, ErrEmptyBody},
		{"path+bytes", MediaMessage{Path: "/x", Bytes: []byte("h")}, ErrEmptyBody},
		{"path+sha256", MediaMessage{Path: "/x", SHA256: "abc"}, ErrEmptyBody},
		{"bytes+sha256", MediaMessage{Bytes: []byte("h"), SHA256: "abc"}, ErrEmptyBody},
		{"all three", MediaMessage{Path: "/x", Bytes: []byte("h"), SHA256: "abc"}, ErrEmptyBody},
		{"bytes at cap", MediaMessage{Bytes: make([]byte, MaxMediaBytes)}, nil},
		{"bytes over cap", MediaMessage{Bytes: make([]byte, MaxMediaBytes+1)}, ErrMessageTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.m.SourceValidate()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want nil; got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v; got %v", tc.want, err)
			}
		})
	}
}

// TestMediaMessage_Validate_Sources ensures full Validate() (recipient + mime
// gates) composes correctly with the new source rules.
func TestMediaMessage_Validate_Sources(t *testing.T) {
	t.Parallel()
	// Bytes source, full happy path.
	if err := (MediaMessage{Recipient: testRecipient, Bytes: []byte("img"), Mime: "image/png"}).Validate(); err != nil {
		t.Errorf("bytes happy: %v", err)
	}
	// SHA256 source, full happy path.
	if err := (MediaMessage{Recipient: testRecipient, SHA256: "deadbeef", Mime: "image/png"}).Validate(); err != nil {
		t.Errorf("sha256 happy: %v", err)
	}
	// Two sources still fails through Validate (source check before mime).
	if err := (MediaMessage{Recipient: testRecipient, Path: "/x", Bytes: []byte("h"), Mime: "image/png"}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("two sources: %v", err)
	}
	// Oversize bytes fails through Validate.
	if err := (MediaMessage{Recipient: testRecipient, Bytes: make([]byte, MaxMediaBytes+1), Mime: "image/png"}).Validate(); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("oversize bytes: %v", err)
	}
	// No source fails through Validate.
	if err := (MediaMessage{Recipient: testRecipient, Mime: "image/png"}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("no source: %v", err)
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
		ListReplyMessage{Recipient: testRecipient, RowID: "r1", ContextStanzaID: "ctx-1", ContextSender: testRecipient},
		ButtonReplyMessage{Recipient: testRecipient, ButtonID: "b1", ContextStanzaID: "ctx-1", ContextSender: testRecipient},
	}
	for _, m := range msgs {
		if m.To() != testRecipient {
			t.Errorf("To() mismatch")
		}
	}
}

func TestListReplyMessage_Validate(t *testing.T) {
	t.Parallel()
	ctxID := MessageID("ctx-stanza-1")
	if err := (ListReplyMessage{Recipient: testRecipient, RowID: "row-7", Title: "Atendente", ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); err != nil {
		t.Errorf("happy: %v", err)
	}
	if err := (ListReplyMessage{Recipient: testRecipient, RowID: "row-7", ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); err != nil {
		t.Errorf("empty title should be allowed: %v", err)
	}
	if err := (ListReplyMessage{RowID: "row-7", ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero recipient: %v", err)
	}
	if err := (ListReplyMessage{Recipient: testRecipient, ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty rowID: %v", err)
	}
	// #161: ContextStanzaID + ContextSender required for the WhatsApp wire.
	if err := (ListReplyMessage{Recipient: testRecipient, RowID: "row-7", ContextSender: testRecipient}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty contextStanzaId: %v", err)
	}
	if err := (ListReplyMessage{Recipient: testRecipient, RowID: "row-7", ContextStanzaID: ctxID}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero contextSender: %v", err)
	}
}

func TestButtonReplyMessage_Validate(t *testing.T) {
	t.Parallel()
	ctxID := MessageID("ctx-stanza-1")
	if err := (ButtonReplyMessage{Recipient: testRecipient, ButtonID: "btn-1", DisplayText: "Yes", Kind: ButtonReplyButtons, ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); err != nil {
		t.Errorf("happy buttons: %v", err)
	}
	if err := (ButtonReplyMessage{Recipient: testRecipient, ButtonID: "tpl-1", Kind: ButtonReplyTemplate, ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); err != nil {
		t.Errorf("happy template (empty display allowed): %v", err)
	}
	if err := (ButtonReplyMessage{ButtonID: "btn-1", ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero recipient: %v", err)
	}
	if err := (ButtonReplyMessage{Recipient: testRecipient, ContextStanzaID: ctxID, ContextSender: testRecipient}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty buttonID: %v", err)
	}
	// #161: ContextStanzaID + ContextSender required for the WhatsApp wire.
	if err := (ButtonReplyMessage{Recipient: testRecipient, ButtonID: "btn-1", ContextSender: testRecipient}).Validate(); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty contextStanzaId: %v", err)
	}
	if err := (ButtonReplyMessage{Recipient: testRecipient, ButtonID: "btn-1", ContextStanzaID: ctxID}).Validate(); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("zero contextSender: %v", err)
	}
}
