package whatsmeow

import (
	"context"
	"testing"
	"time"

	waClient "go.mau.fi/whatsmeow"
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

// TestPersistConversation_PseudoChatIsNotStored is the spec 115 FR-115-3
// proof for the history-sync path: persistConversation refuses to write a
// pseudo-chat conversation and keeps the inserted count honest (0), while a
// conversation still inserts exactly one row. History-sync chat JIDs come
// straight off the wire as strings, so this path — unlike Send/LoadMore —
// can actually meet a pseudo-chat.
func TestPersistConversation_PseudoChatIsNotStored(t *testing.T) {
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
			t.Cleanup(func() { _ = a.Close() })

			inserted := a.persistConversation(context.Background(), tc.chat, hsConversation(&waE2E.Message{Conversation: new("plain text")}))
			if tc.wantStore {
				if inserted != 1 {
					t.Fatalf("inserted = %d, want 1 for %s", inserted, tc.chat)
				}
				if len(hist.rawCalls) != 1 || hist.rawCalls[0].ChatJID != tc.chat {
					t.Fatalf("rawCalls = %+v, want exactly one for %s", hist.rawCalls, tc.chat)
				}
			} else {
				if inserted != 0 {
					t.Fatalf("inserted = %d, want 0 for pseudo-chat %s", inserted, tc.chat)
				}
				if len(hist.rawCalls) != 0 {
					t.Fatalf("rawCalls = %+v, want 0 for pseudo-chat %s", hist.rawCalls, tc.chat)
				}
			}
		})
	}
}

// TestSend_ServerNoticeChatIsNotPersisted is the spec 115 FR-115-3 proof for
// the outbound path: a send to the server notice chat still goes out (the
// guard is retention, not delivery) but is not retained, while a conversation
// send persists. The server notice chat is the only pseudo-chat shape
// domain.Parse will construct — @broadcast is refused on sight.
func TestSend_ServerNoticeChatIsNotPersisted(t *testing.T) {
	t.Parallel()

	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	hist := &auditHistoryContainer{}
	a.history = hist
	t.Cleanup(func() { _ = a.Close() })

	send := func(to string) {
		t.Helper()
		fc.SendResp = waClient.SendResponse{ID: "wamid." + to, Timestamp: fixedNowFn()}
		if _, err := a.Send(context.Background(), domain.TextMessage{Recipient: domain.MustJID(to), Body: "hi"}); err != nil {
			t.Fatalf("Send(%s): %v", to, err)
		}
	}

	send("0@s.whatsapp.net")
	if len(fc.SentMessages) != 1 {
		t.Fatalf("send must still go out; SentMessages = %d, want 1", len(fc.SentMessages))
	}
	if len(hist.rawCalls) != 0 {
		t.Fatalf("rawCalls = %d, want 0 for pseudo-chat send", len(hist.rawCalls))
	}

	send("558134658209@s.whatsapp.net")
	if len(hist.rawCalls) != 1 {
		t.Fatalf("rawCalls = %d, want 1 for conversation send", len(hist.rawCalls))
	}
}

// TestLoadMore_ServerNoticeChatIsNotPersisted is the spec 115 FR-115-3 proof
// for the persist-late (HS6) path: a remote page delivered for a pseudo-chat
// is returned to the caller but never written to the local store, while a
// conversation page persists-late exactly once.
//
// Delivery is injected synchronously from the fake's request-build hook: the
// pending entry is registered before sendHistoryRequest runs, so the buffered
// response is waiting before LoadMore reaches its select. The round trip has
// no wall-clock wait and no scheduling dependence — deliberately not a
// synctest bubble, whose auto-advancing clock would fire
// historyRequestTimeout before the delivery could be made.
func TestLoadMore_ServerNoticeChatIsNotPersisted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		chat      string
		wantStore bool
	}{
		{"server_notice", "0@s.whatsapp.net", false},
		{"conversation", "558134658209@s.whatsapp.net", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeClient()
			fc.ConnectedFlag = true
			a := newTestAdapter(t, fc)
			hist := &auditHistoryContainer{}
			a.history = hist
			t.Cleanup(func() { _ = a.Close() })

			chat := domain.MustJID(tc.chat)
			// A stored anchor is required for the remote pull to happen at
			// all: without one, sendHistoryRequest treats the request as a
			// no-op (PR #222) and the hook below would never run.
			ref := anchorRef(chat)
			hist.oldestAnchor = &ref
			delivered := false
			fc.OnBuildHS = func() {
				delivered = a.resolveHistoryReq([]domain.Message{domain.TextMessage{Recipient: chat, Body: "remote"}})
			}

			got, err := a.LoadMore(context.Background(), chat, "", 5)
			if err != nil {
				t.Fatalf("LoadMore: %v", err)
			}
			if !delivered {
				t.Fatal("resolveHistoryReq did not deliver to the pending LoadMore")
			}
			if len(got) != 1 {
				t.Fatalf("delivered %d messages, want 1 (delivery is not retention)", len(got))
			}
			if tc.wantStore {
				if len(hist.inserted) != 1 {
					t.Fatalf("persist-late batches = %d, want 1", len(hist.inserted))
				}
			} else if len(hist.inserted) != 0 {
				t.Fatalf("persist-late batches = %d, want 0 for pseudo-chat %s", len(hist.inserted), tc.chat)
			}
		})
	}
}
