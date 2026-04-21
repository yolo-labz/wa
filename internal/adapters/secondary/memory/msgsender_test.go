package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/domain"
)

func TestSendReplyContract(t *testing.T) {
	t.Parallel()
	a := memory.New(nil)
	chat := domain.MustJID("5511999999999")
	msg := domain.TextMessage{Recipient: chat, Body: "hi"}

	if _, err := a.SendReply(context.Background(), "", msg); !errors.Is(err, memory.ErrZeroQuotedID) {
		t.Fatalf("want ErrZeroQuotedID, got %v", err)
	}

	id, err := a.SendReply(context.Background(), "orig-42", msg)
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if id == "" {
		t.Error("SendReply returned empty id")
	}

	links := a.Replies()
	if len(links) != 1 {
		t.Fatalf("Replies() len=%d, want 1", len(links))
	}
	if links[0].QuotedID != "orig-42" {
		t.Errorf("QuotedID = %q, want orig-42", links[0].QuotedID)
	}
	if links[0].ReplyID != id {
		t.Errorf("ReplyID mismatch: got %q want %q", links[0].ReplyID, id)
	}

	if got := a.Sent(); len(got) != 1 {
		t.Errorf("Sent() len=%d, want 1", len(got))
	}
}
