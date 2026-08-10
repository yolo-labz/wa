package whatsmeow

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// pollTestJID parses a well-formed user JID for the poll adapter tests.
func pollTestJID(t *testing.T) domain.JID {
	t.Helper()
	jid, err := domain.Parse("5511999998888@s.whatsapp.net")
	if err != nil {
		t.Fatalf("parse test JID: %v", err)
	}
	return jid
}

func newPollManager(t *testing.T, fc *fakeWhatsmeowClient) *PollManagerAdapter {
	t.Helper()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	pm, err := a.NewPollManagerFor()
	if err != nil {
		t.Fatalf("NewPollManagerFor: %v", err)
	}
	return pm
}

// TestPollCreate_BuildsAndSends: the adapter hands the question, the option
// names and the selectable count to whatsmeow's BuildPollCreation, sends the
// result, and returns the sent message's id.
func TestPollCreate_BuildsAndSends(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	pm := newPollManager(t, fc)

	id, err := pm.Create(context.Background(), pollTestJID(t), "lunch?",
		[]string{"pizza", "sushi"}, 1)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Error("empty MessageID on success")
	}
	if len(fc.PollCreateCalls) != 1 {
		t.Fatalf("BuildPollCreation calls = %d, want 1", len(fc.PollCreateCalls))
	}
	got := fc.PollCreateCalls[0]
	if got.Name != "lunch?" {
		t.Errorf("question = %q, want %q", got.Name, "lunch?")
	}
	if len(got.Options) != 2 || got.Options[0] != "pizza" || got.Options[1] != "sushi" {
		t.Errorf("options = %v, want [pizza sushi]", got.Options)
	}
	if got.Selectable != 1 {
		t.Errorf("selectable = %d, want 1", got.Selectable)
	}
	if len(fc.SentMessages) != 1 {
		t.Errorf("SendMessage calls = %d, want 1", len(fc.SentMessages))
	}
}

// TestPollCreate_RefusedBeforeIO: every state the adapter must reject without
// building or sending anything — offline, zero recipient, dead context. Table
// driven so the "assert no IO happened" half exists once.
func TestPollCreate_RefusedBeforeIO(t *testing.T) {
	cases := map[string]struct {
		connected bool
		ctx       func(t *testing.T) context.Context
		chat      func(t *testing.T) domain.JID
		want      error
	}{
		"disconnected": {
			connected: false,
			ctx:       func(*testing.T) context.Context { return context.Background() },
			chat:      pollTestJID,
			want:      domain.ErrDisconnected,
		},
		"zero JID": {
			connected: true,
			ctx:       func(*testing.T) context.Context { return context.Background() },
			chat:      func(*testing.T) domain.JID { return domain.JID{} },
			want:      domain.ErrInvalidJID,
		},
		"cancelled ctx": {
			connected: true,
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			chat: pollTestJID,
			want: context.Canceled,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fc := newFakeClient()
			fc.ConnectedFlag = tc.connected
			pm := newPollManager(t, fc)

			_, err := pm.Create(tc.ctx(t), tc.chat(t), "q", []string{"a", "b"}, 1)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(fc.PollCreateCalls) != 0 || len(fc.SentMessages) != 0 {
				t.Errorf("reached whatsmeow: build=%d send=%d",
					len(fc.PollCreateCalls), len(fc.SentMessages))
			}
		})
	}
}

// TestPollVote_StillUpstreamError pins the documented gap: votes need the
// poll's sender and option names, which the port does not carry.
func TestPollVote_StillUpstreamError(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	pm := newPollManager(t, fc)

	err := pm.Vote(context.Background(), pollTestJID(t), domain.MessageID("3EB0abc"), []int{0})
	if !errors.Is(err, domain.ErrUpstreamError) {
		t.Fatalf("err = %v, want ErrUpstreamError", err)
	}
}
