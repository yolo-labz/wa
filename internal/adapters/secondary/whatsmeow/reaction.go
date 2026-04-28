package whatsmeow

import (
	"time"

	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// buildReactionMessage maps a domain.ReactionMessage onto a whatsmeow
// *waE2E.Message with a ReactionMessage payload. Feature 018 T1-08 / R-02
// closes the gap where commit 4 returned "not implemented".
//
// Empty Emoji is the WhatsApp sentinel for "remove the reaction"; Validate()
// on the domain side explicitly allows it, so we forward an empty Text
// pointer (proto.String("")) unchanged.
//
// For v0 we assume the target message is either outbound (FromMe=true) or
// from a 1:1 chat — the common case. Reactions to messages sent by someone
// else in a group require a Participant field populated from a ThreadReader
// lookup; that refinement is out of scope for T1-09 and tracked on the
// parity backlog.
func buildReactionMessage(m domain.ReactionMessage, nowFn func() time.Time) *waE2E.Message {
	now := nowFn().UnixMilli()
	return &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{
			Key: &waCommon.MessageKey{
				RemoteJID: proto.String(m.Recipient.String()),
				FromMe:    proto.Bool(true),
				ID:        proto.String(string(m.TargetID)),
			},
			Text:              proto.String(m.Emoji),
			SenderTimestampMS: proto.Int64(now),
		},
	}
}
