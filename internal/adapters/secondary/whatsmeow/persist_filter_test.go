package whatsmeow

import (
	"context"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestPersistInbound_PseudoChatIsNotStored is the spec 115 FR-115-2 proof: an
// inbound message on a pseudo-chat (WhatsApp Status, broadcast lists, server
// notices) reaches persistInboundMessage and writes nothing to the store,
// while the FR-028 contact mirror still runs — a status poster is a contact,
// and the mirror is not message retention. A conversation chat stores exactly
// one row, so the guard cannot pass by dropping everything.
func TestPersistInbound_PseudoChatIsNotStored(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		chat      string
		wantStore bool
	}{
		{"status_updates", "status@broadcast", false},
		{"broadcast_list", "1788957129@broadcast", false},
		{"server_notice", "0@s.whatsapp.net", false},
		{"conversation", "558134658209@s.whatsapp.net", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeClient()
			a := newTestAdapter(t, fc)
			hist := &auditHistoryContainer{}
			a.history = hist
			mirrored := 0
			a.pushNameSink = func(_ context.Context, _ domain.JID, _ string) { mirrored++ }
			t.Cleanup(func() { _ = a.Close() })

			chat, err := waTypes.ParseJID(tc.chat)
			if err != nil {
				t.Fatalf("fixture chat %q: %v", tc.chat, err)
			}
			sender, err := waTypes.ParseJID("558199999999@s.whatsapp.net")
			if err != nil {
				t.Fatalf("fixture sender: %v", err)
			}

			a.persistInboundMessage(&waEvents.Message{
				Info: waTypes.MessageInfo{
					MessageSource: waTypes.MessageSource{Chat: chat, Sender: sender},
					ID:            "MSG-115-1",
					Timestamp:     time.Unix(1_700_000_000, 0),
					PushName:      "Contato",
				},
				Message: &waE2E.Message{Conversation: proto.String("status text")},
			})

			if tc.wantStore {
				if len(hist.rawCalls) != 1 {
					t.Fatalf("rawCalls = %d, want 1 for %s", len(hist.rawCalls), tc.chat)
				}
				if got := hist.rawCalls[0].ChatJID; got != tc.chat {
					t.Errorf("stored chat = %q, want %q", got, tc.chat)
				}
			} else if len(hist.rawCalls) != 0 {
				t.Fatalf("rawCalls = %d, want 0 for pseudo-chat %s", len(hist.rawCalls), tc.chat)
			}

			if mirrored != 1 {
				t.Errorf("pushNameSink calls = %d, want 1 (FR-028 mirror is not retention)", mirrored)
			}
		})
	}
}
