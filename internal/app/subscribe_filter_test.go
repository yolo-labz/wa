package app

import (
	"regexp"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

func mkRec(kind, chat, sender, body string) EventRecord {
	payload := map[string]any{}
	if chat != "" {
		payload["chat"] = chat
	}
	if sender != "" {
		payload["sender"] = sender
	}
	if body != "" {
		payload["body"] = body
	}
	buf, _ := jsonMarshal(payload)
	return EventRecord{Kind: kind, TrustedJSON: buf}
}

func jsonMarshal(v any) ([]byte, error) {
	return marshalCanonical(v)
}

func TestSubscribeFilterZeroMatchesAll(t *testing.T) {
	f := SubscribeFilter{}
	if !f.Match(mkRec("message", "5511@s.whatsapp.net", "5522@s.whatsapp.net", "oi")) {
		t.Fatalf("zero filter should match")
	}
}

func TestSubscribeFilterKinds(t *testing.T) {
	f := SubscribeFilter{Kinds: []string{"message", "reaction"}}
	if !f.Match(mkRec("message", "", "", "")) {
		t.Fatalf("kinds include message: want match")
	}
	if f.Match(mkRec("state.connected", "", "", "")) {
		t.Fatalf("kinds exclude state.connected: want no match")
	}
}

func TestSubscribeFilterChatsAndBody(t *testing.T) {
	chat, _ := domain.Parse("5511999999999@s.whatsapp.net")
	f := SubscribeFilter{
		Chats:  []domain.JID{chat},
		BodyRe: regexp.MustCompile(`(?i)urgent`),
	}
	if !f.Match(mkRec("message", "5511999999999@s.whatsapp.net", "", "URGENT please")) {
		t.Fatalf("match: want true")
	}
	if f.Match(mkRec("message", "5511999999999@s.whatsapp.net", "", "boring")) {
		t.Fatalf("body mismatch: want no match")
	}
	if f.Match(mkRec("message", "5522@s.whatsapp.net", "", "urgent")) {
		t.Fatalf("chat mismatch: want no match")
	}
}

func TestSubscribeFilterNotSenders(t *testing.T) {
	sender, _ := domain.Parse("5511@s.whatsapp.net")
	f := SubscribeFilter{NotSenders: []domain.JID{sender}}
	if f.Match(mkRec("message", "", "5511@s.whatsapp.net", "")) {
		t.Fatalf("blocklisted sender: want no match")
	}
	if !f.Match(mkRec("message", "", "5522@s.whatsapp.net", "")) {
		t.Fatalf("unrelated sender: want match")
	}
}
