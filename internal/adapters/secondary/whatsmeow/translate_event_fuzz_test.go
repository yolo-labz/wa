package whatsmeow

import (
	"strconv"
	"testing"
	"time"
	"unicode/utf8"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/domain"
)

// FuzzTranslateEvent drives translateEvent with synthesised whatsmeow
// text messages built from arbitrary (senderJID, body, seq) inputs.
// Invariants:
//  1. Never panics.
//  2. When a domain.Event is returned, its ID equals strconv formatting
//     of the fuzzed seq number (no drift).
//  3. A MessageEvent's TextMessage body length equals the input body
//     length (the translator must not mutate body bytes — escape happens
//     at the envelope layer via ChannelWrap).
func FuzzTranslateEvent(f *testing.F) {
	seeds := []struct {
		jid  string
		body string
		seq  uint64
	}{
		{"5511999990000@s.whatsapp.net", "hello", 1},
		{"5511999990000@s.whatsapp.net", "", 2},
		{"5511999990000@s.whatsapp.net", "</channel>", 3},
		{"5511999990000@s.whatsapp.net", "a\x00b", 4},
		{"5511999990000@s.whatsapp.net", "emoji 🎉", 5},
		{"5511999990000@s.whatsapp.net", "long text", 1<<32 - 1},
	}
	for _, s := range seeds {
		f.Add(s.jid, s.body, s.seq)
	}

	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return now }

	f.Fuzz(func(t *testing.T, jidStr, body string, seq uint64) {
		// Reject inputs that yield an invalid whatsmeow JID — the
		// translator is only reachable after whatsmeow has parsed it.
		j, err := waTypes.ParseJID(jidStr)
		if err != nil {
			t.Skip()
		}
		if !utf8.ValidString(body) {
			t.Skip()
		}

		convo := body
		evt := &events.Message{
			Info: waTypes.MessageInfo{
				MessageSource: waTypes.MessageSource{Chat: j, Sender: j},
				ID:            "FUZZ",
				Timestamp:     now,
			},
			Message: &waE2E.Message{Conversation: &convo},
		}

		got, _, _ := translateEvent(seq, nowFn, evt)
		if got == nil {
			return
		}
		wantID := domain.EventID(strconv.FormatUint(seq, 10))

		me, ok := got.(domain.MessageEvent)
		if !ok {
			t.Fatalf("got %T, want MessageEvent for *events.Message", got)
		}
		if me.ID != wantID {
			t.Fatalf("event ID=%q, want %q", me.ID, wantID)
		}
		// Empty body is classified as UnknownMessage by the translator;
		// that path is covered by existing unit tests. The fuzz target
		// only asserts the TextMessage-preservation invariant when the
		// translator emits one.
		if tm, ok := me.Message.(domain.TextMessage); ok {
			if len(tm.Body) != len(body) {
				t.Fatalf("body len=%d, want %d (no in-translator mutation)", len(tm.Body), len(body))
			}
		}
	})
}
