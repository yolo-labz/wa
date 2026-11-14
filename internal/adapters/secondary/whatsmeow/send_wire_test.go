package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	waClient "go.mau.fi/whatsmeow"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

const wireTestChat = "5511911111111@s.whatsapp.net"

func shrinkWireBudget(t *testing.T, d time.Duration) {
	t.Helper()
	old := sendWireBudget
	sendWireBudget = d
	t.Cleanup(func() { sendWireBudget = old })
}

func sentCount(fc *fakeWhatsmeowClient) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.SentMessages)
}

// A cancel after the frame left must not abort the send: the send finishes
// and the caller gets the id (F4 (a)).
func TestSendDetached_CancelAfterFrameDoesNotAbortSend(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendGate = make(chan struct{})
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	type out struct {
		id  domain.MessageID
		err error
	}
	done := make(chan out, 1)
	go func() {
		id, err := a.Send(ctx, domain.TextMessage{Recipient: mustParseJIDT(wireTestChat), Body: "hi"})
		done <- out{id, err}
	}()
	for sentCount(fc) == 0 {
		<-time.After(2 * time.Millisecond)
	}
	cancel()
	select {
	case o := <-done:
		t.Fatalf("Send returned after the cancel, before the wire call finished: %+v", o)
	case <-time.After(60 * time.Millisecond):
	}
	close(fc.SendGate)
	o := <-done
	if o.err != nil || o.id == "" {
		t.Fatalf("detached send: id=%q err=%v, want the id and no error", o.id, o.err)
	}
}

func TestSendDetached_UncertainOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(fc *fakeWhatsmeowClient)
		want  bool
	}{
		{"ack timeout", func(fc *fakeWhatsmeowClient) { fc.SendErr = waClient.ErrMessageTimedOut }, true},
		{"wrapped ack timeout", func(fc *fakeWhatsmeowClient) { fc.SendErr = errors.Join(errors.New("x"), waClient.ErrMessageTimedOut) }, true},
		{"wire budget ends while waiting for the ack", func(fc *fakeWhatsmeowClient) { fc.SendBlocks, fc.SendWrote = true, true }, true},
		{"wire budget ends before any write (prekey lookup)", func(fc *fakeWhatsmeowClient) { fc.SendBlocks = true }, false},
		{"ctx error before any write", func(fc *fakeWhatsmeowClient) { fc.SendErr = context.DeadlineExceeded }, false},
		{"ctx error after the write", func(fc *fakeWhatsmeowClient) { fc.SendErr, fc.SendWrote = context.Canceled, true }, true},
		{"disconnected after the write", func(fc *fakeWhatsmeowClient) {
			fc.SendErr = &waClient.DisconnectedError{Action: "message send"}
		}, true},
		{"disconnected on the retry", func(fc *fakeWhatsmeowClient) {
			fc.SendErr = &waClient.DisconnectedError{Action: "message send (retry)"}
		}, true},
		{"disconnected during a prekey lookup is pre-write", func(fc *fakeWhatsmeowClient) {
			fc.SendErr = &waClient.DisconnectedError{Action: "info query"}
		}, false},
		{"not connected on the re-send after the write", func(fc *fakeWhatsmeowClient) {
			fc.SendErr, fc.SendWrote = waClient.ErrNotConnected, true
		}, true},
		{"not connected before any write", func(fc *fakeWhatsmeowClient) { fc.SendErr = waClient.ErrNotConnected }, false},
		{"the write itself failed", func(fc *fakeWhatsmeowClient) {
			fc.SendErr, fc.SendWrote = fmt.Errorf("failed to send message node: %w", waClient.ErrNotConnected), true
		}, false},
		{"re-send write error after the first write", func(fc *fakeWhatsmeowClient) {
			fc.SendErr, fc.SendWrote = errors.New("write tcp: broken pipe"), true
		}, true},
		{"iq timeout after the write", func(fc *fakeWhatsmeowClient) {
			fc.SendErr, fc.SendWrote = waClient.ErrIQTimedOut, true
		}, true},
		{"server refusal after the write is definite", func(fc *fakeWhatsmeowClient) {
			fc.SendErr, fc.SendWrote = fmt.Errorf("%w 479", waClient.ErrServerReturnedError), true
		}, false},
		{"server error is a plain failure", func(fc *fakeWhatsmeowClient) { fc.SendErr = waClient.ErrServerReturnedError }, false},
		{"other error is a plain failure", func(fc *fakeWhatsmeowClient) { fc.SendErr = errors.New("boom") }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shrinkWireBudget(t, 40*time.Millisecond)
			fc := newFakeClient()
			fc.ConnectedFlag = true
			fc.SendErrID = "WAID-1"
			c.setup(fc)
			a := newTestAdapter(t, fc)
			t.Cleanup(func() { _ = a.Close() })
			// The caller's own deadline must not matter; it only bounds a regression.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := a.Send(ctx, domain.TextMessage{Recipient: mustParseJIDT(wireTestChat), Body: "hi"})
			if err == nil {
				t.Fatal("want an error")
			}
			var u *domain.SendUncertainError
			got := errors.Is(err, domain.ErrSendUncertain)
			if got != c.want {
				t.Fatalf("ErrSendUncertain=%v want %v (err %v)", got, c.want, err)
			}
			if c.want && (!errors.As(err, &u) || u.MessageID != "WAID-1") {
				t.Fatalf("uncertain error must carry the library's id: %+v", u)
			}
			if got := auditDecisions(a); (got == "uncertain") != c.want {
				t.Fatalf("audit decision %q, uncertain=%v", got, c.want)
			}
		})
	}
}

// Revoke(everyone), edit and poll.create share the detached send (F4).
func TestSendDetached_ModeratorAndPollShareIt(t *testing.T) {
	shrinkWireBudget(t, 40*time.Millisecond)
	chat := mustParseJIDT(wireTestChat)
	now := fixedNowFn()
	h := &fakeModHistory{MetaTS: now.Add(-time.Minute).Unix(), MetaBody: "orig"}
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendErr, fc.SendErrID = waClient.ErrMessageTimedOut, "WAID-2"
	m, err := NewModerator(fc, h, newAuditRing(16), discardLogger(), fixedNowFn)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	calls := map[string]error{
		"edit":   m.Edit(context.Background(), chat, "M1", "new"),
		"revoke": m.Revoke(context.Background(), chat, "M2", domain.RevokeEveryone),
	}
	_, calls["poll.create"] = (&PollManagerAdapter{adapter: a}).Create(context.Background(), chat, "q", []string{"a", "b"}, 1)
	for name, err := range calls {
		if !errors.Is(err, domain.ErrSendUncertain) {
			t.Errorf("%s: %v, want ErrSendUncertain", name, err)
		}
	}
	if len(h.StampEditCalls)+len(h.StampRevokeCalls) != 0 {
		t.Error("an uncertain edit/revoke must not stamp the local row as done")
	}
}

func auditDecisions(a *Adapter) string {
	evs := a.auditBuf.Snapshot()
	if len(evs) == 0 {
		return ""
	}
	return evs[len(evs)-1].Decision
}

// A success never becomes uncertain, even when the response carries the
// write timing the uncertain classification keys on.
func TestSendDetached_SuccessWithWriteTimingIsNotUncertain(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp.DebugTimings.Send = time.Millisecond
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	if id, err := a.Send(context.Background(), domain.TextMessage{Recipient: mustParseJIDT(wireTestChat), Body: "hi"}); err != nil || id == "" {
		t.Fatalf("id=%q err=%v, want a plain success", id, err)
	}
}

// The wire budget is the 90 s the manual and the executor's 2 min cap assume.
func TestSendWireBudgetIs90Seconds(t *testing.T) {
	if sendWireBudget != 90*time.Second {
		t.Fatalf("sendWireBudget = %v, want 90s", sendWireBudget)
	}
}
