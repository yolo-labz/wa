package whatsmeow

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

type liveHistoryCapture struct {
	auditHistoryContainer
	received time.Time
	ctx      context.Context
	record   domain.HistoryRecord
}

func (h *liveHistoryCapture) InsertLive(ctx context.Context, rec domain.HistoryRecord, received time.Time) error {
	h.received, h.ctx, h.record = received, ctx, rec
	return h.InsertRawInteractive(ctx, rec)
}

func TestPersistInbound_HealthUsesLocalReceiptAndDaemonContext(t *testing.T) {
	a := newTestAdapter(t, newFakeClient())
	t.Cleanup(func() { _ = a.Close() })
	h := &liveHistoryCapture{}
	a.history = h
	now := time.Date(2026, 11, 8, 22, 0, 0, 0, time.FixedZone("BRT", -3*60*60))
	a.nowFn = func() time.Time { return now }
	evt := &waEvents.Message{Info: waTypes.MessageInfo{MessageSource: waTypes.MessageSource{Chat: waTypes.NewJID("15551234567", waTypes.DefaultUserServer), Sender: waTypes.NewJID("15557654321", waTypes.DefaultUserServer)}, ID: "fake-receipt", Timestamp: time.Unix(1, 0)}, Message: &waE2E.Message{Conversation: proto.String("fixture")}}
	a.persistInboundMessage(evt)
	if !h.received.Equal(now) || h.ctx != a.clientCtx || len(h.rawCalls) != 1 || h.record.Timestamp != 1 {
		t.Fatalf("receipt/record mismatch: received=%v ctx=%v records=%+v", h.received, h.ctx, h.rawCalls)
	}
}
