package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/yolo-labz/wa/internal/domain"
)

// ErrZeroQuotedID is returned by SendReply when the caller passes an empty
// MessageID (contract RS2).
var ErrZeroQuotedID = errors.New("memory: zero quoted message id")

// SendReply implements app.ReplySender. It records the reply as an ordinary
// Sent message so Sent() assertions in tests remain stable, and captures the
// quotedID on a parallel slice the test surface can inspect.
func (a *Adapter) SendReply(ctx context.Context, quotedID domain.MessageID, msg domain.Message) (domain.MessageID, error) {
	if quotedID == "" {
		return "", fmt.Errorf("MessageSender.SendReply: %w", ErrZeroQuotedID)
	}
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("MessageSender.SendReply: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sentSeq++
	id := domain.MessageID("mem-reply-" + strconv.Itoa(a.sentSeq))
	a.sent = append(a.sent, msg)
	a.replyTo = append(a.replyTo, replyLink{replyID: id, quotedID: quotedID})
	return id, nil
}

// Replies returns a snapshot of reply-links recorded by SendReply.
// Test-only accessor.
func (a *Adapter) Replies() []ReplyLink {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ReplyLink, len(a.replyTo))
	for i, r := range a.replyTo {
		out[i] = ReplyLink{ReplyID: r.replyID, QuotedID: r.quotedID}
	}
	return out
}

// ReplyLink is the test-visible view of a SendReply call.
type ReplyLink struct {
	ReplyID  domain.MessageID
	QuotedID domain.MessageID
}

type replyLink struct {
	replyID  domain.MessageID
	quotedID domain.MessageID
}
