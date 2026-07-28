package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// hsConversation wraps inner messages in the WebMessageInfo/HistorySyncMsg
// envelope the on-demand decoder receives from the server.
func hsConversation(msgs ...*waE2E.Message) *waHistorySync.Conversation {
	conv := &waHistorySync.Conversation{}
	for i, m := range msgs {
		conv.Messages = append(conv.Messages, &waHistorySync.HistorySyncMsg{
			Message: &waWeb.WebMessageInfo{
				Key:     &waCommon.MessageKey{ID: proto.String("m" + string(rune('0'+i)))},
				Message: m,
			},
		})
	}
	return conv
}

// TestRouteOnDemandResponsePreservesVariants is the regression: the
// decoder built a domain.TextMessage for every message it returned, so
// LoadMore reported an image as text carrying its caption, a sticker as
// text carrying nothing, and dropped reactions entirely. The sum type is
// the whole mechanism a caller has for telling these apart.
func TestRouteOnDemandResponsePreservesVariants(t *testing.T) {
	t.Parallel()

	const chatJID = "12025550100@s.whatsapp.net"
	a := &Adapter{}
	pending := &pendingHistoryReq{chatJID: chatJID, msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store("req-1", pending)

	a.routeOnDemandResponse(chatJID, hsConversation(
		&waE2E.Message{Conversation: proto.String("plain text")},
		&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Mimetype: proto.String("image/jpeg"),
			Caption:  proto.String("a caption"),
		}},
		&waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}},
		&waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
			Key:  &waCommon.MessageKey{ID: proto.String("target-1")},
			Text: proto.String("👍"),
		}},
		&waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(-8.0476),
			DegreesLongitude: proto.Float64(-34.877),
		}},
	))

	var got []domain.Message
	select {
	case got = <-pending.msgs:
	default:
		t.Fatal("nothing delivered to the pending LoadMore caller")
	}
	if len(got) != 5 {
		t.Fatalf("delivered %d messages, want 5: %#v", len(got), got)
	}

	if _, ok := got[0].(domain.TextMessage); !ok {
		t.Errorf("text message came back as %T", got[0])
	}
	img, ok := got[1].(domain.MediaMessage)
	if !ok {
		t.Errorf("image came back as %T, want domain.MediaMessage", got[1])
	} else if img.Caption != "a caption" {
		t.Errorf("image caption = %q, want %q", img.Caption, "a caption")
	}
	if _, ok := got[2].(domain.StickerMessage); !ok {
		t.Errorf("sticker came back as %T, want domain.StickerMessage", got[2])
	}
	react, ok := got[3].(domain.ReactionMessage)
	if !ok {
		t.Errorf("reaction came back as %T, want domain.ReactionMessage", got[3])
	} else if react.TargetID != "target-1" || react.Emoji != "👍" {
		t.Errorf("reaction = (%q, %q), want (target-1, 👍)", react.TargetID, react.Emoji)
	}
	if _, ok := got[4].(domain.LocationPin); !ok {
		t.Errorf("location came back as %T, want domain.LocationPin", got[4])
	}
}

// TestRouteOnDemandResponseSkipsProtocolMessages keeps the filter honest:
// machinery the server sends alongside real messages must not surface in
// a LoadMore page, and a batch of nothing but machinery must not wake a
// waiting caller with an empty slice.
func TestRouteOnDemandResponseSkipsProtocolMessages(t *testing.T) {
	t.Parallel()

	const chatJID = "12025550100@s.whatsapp.net"
	a := &Adapter{}
	pending := &pendingHistoryReq{chatJID: chatJID, msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store("req-1", pending)

	a.routeOnDemandResponse(chatJID, hsConversation(
		&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}},
		&waE2E.Message{SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{}},
	))

	select {
	case got := <-pending.msgs:
		t.Fatalf("protocol-only batch delivered %d messages: %#v", len(got), got)
	default:
	}
}

// TestRouteOnDemandResponseUnparseableChat pins the batch-level failure:
// the chat JID is a property of the conversation, so one that does not
// parse makes the whole response undeliverable rather than silently
// yielding a page of messages addressed to the zero JID.
func TestRouteOnDemandResponseUnparseableChat(t *testing.T) {
	t.Parallel()

	a := &Adapter{}
	pending := &pendingHistoryReq{chatJID: "not a jid", msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store("req-1", pending)

	a.routeOnDemandResponse("not a jid", hsConversation(
		&waE2E.Message{Conversation: proto.String("plain text")},
	))

	select {
	case got := <-pending.msgs:
		t.Fatalf("unparseable chat delivered %d messages: %#v", len(got), got)
	default:
	}
}
