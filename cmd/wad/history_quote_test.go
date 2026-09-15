package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"google.golang.org/protobuf/encoding/protowire"
)

func quotedTextProto(t *testing.T, body, quotedID string) []byte {
	t.Helper()
	// Neutral wire fixture: Message.extendedTextMessage=6, text=1,
	// ExtendedTextMessage.contextInfo=17, ContextInfo.stanzaID=1.
	quoted := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), quotedID)
	text := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), body)
	text = protowire.AppendBytes(protowire.AppendTag(text, 17, protowire.BytesType), quoted)
	return protowire.AppendBytes(protowire.AppendTag(nil, 6, protowire.BytesType), text)
}

func TestHistoryAndExportExposeOnlySameChatValidatedQuote(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-a", true: "first-b"}[reverse], func(t *testing.T) {
			testHistoryQuotes(t, reverse)
		})
	}
}

func testHistoryQuotes(t *testing.T, reverse bool) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	chatA, chatB := "11111111@s.whatsapp.net", "22222222@s.whatsapp.net"
	insert := func(chat, id, body, media string, raw []byte, ts int64) {
		t.Helper()
		if err := store.InsertRaw(ctx, chat, chat, id, ts, body, media, "", "sender", false, raw, "", ""); err != nil {
			t.Fatalf("insert %s/%s: %v", chat, id, err)
		}
	}
	chats := []string{chatA, chatB}
	if reverse {
		chats[0], chats[1] = chats[1], chats[0]
	}
	for i, chat := range chats {
		insert(chat, "PHOTO", "", "image/jpeg", []byte(chat), int64(i+1))
		insert(chat, "STICKER", "", "image/webp", []byte(chat), int64(i+1))
	}
	insert(chatB, "ONLY-B", "", "image/jpeg", nil, 3)
	insert(chatA, "GOOD", "reply body", "", quotedTextProto(t, "reply body", "PHOTO"), 10)
	insert(chatA, "NOQUOTE", "plain body", "", quotedTextProto(t, "plain body", ""), 11)
	insert(chatA, "MALFORMED", "bad body", "", quotedTextProto(t, "bad body", "</channel>"), 12)
	insert(chatA, "MISSING", "missing body", "", quotedTextProto(t, "missing body", "ABSENT"), 13)
	insert(chatA, "CROSS", "cross body", "", quotedTextProto(t, "cross body", "ONLY-B"), 14)
	insert(chatA, "STICKER-REPLY", "sticker body", "", quotedTextProto(t, "sticker body", "STICKER"), 15)
	insert(chatA, "LEGACY", "legacy body", "", nil, 16)
	insert(chatA, "CORRUPT", "corrupt body", "", []byte{0xff}, 17)

	handlers := map[string]func(context.Context, json.RawMessage) (json.RawMessage, error){
		"history": makeHistoryHandler(store),
		"export":  makeExportHandler(store, nil),
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			raw, err := handler(ctx, json.RawMessage(`{"chat":"`+chatA+`","limit":50}`))
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			var env struct {
				Messages []wireMessage `json:"messages"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			byID := make(map[string]wireMessage, len(env.Messages))
			for _, msg := range env.Messages {
				byID[msg.MessageID] = msg
			}
			if got := byID["GOOD"].QuotedMessageID; got != "PHOTO" {
				t.Fatalf("good quotedMessageId = %q", got)
			}
			if byID["STICKER-REPLY"].QuotedMessageID != "STICKER" {
				t.Fatal("sticker quote was lost")
			}
			for _, id := range []string{"NOQUOTE", "LEGACY", "CORRUPT"} {
				if byID[id].QuotedMessageID != "" || len(byID[id].RejectedIDs) != 0 {
					t.Fatalf("%s gained quote state", id)
				}
			}
			if strings.Contains(string(raw), `"quotedMessageId":""`) || strings.Contains(string(raw), `"rawProto"`) {
				t.Fatal("response exposed empty quote or raw proto")
			}
			for _, id := range []string{"MALFORMED", "MISSING", "CROSS"} {
				if byID[id].QuotedMessageID != "" || len(byID[id].RejectedIDs) != 1 {
					t.Errorf("%s must omit and reject quotedMessageId: %+v", id, byID[id])
				}
			}
			if got := byID["GOOD"]; got.Body != "" || !strings.Contains(got.Channel, "reply body") {
				t.Fatalf("inbound wrapping changed: %+v", got)
			}
		})
	}
}
