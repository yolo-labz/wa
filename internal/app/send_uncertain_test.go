package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// uncertainSender is the adapter's answer to an ack that never came.
type uncertainSender struct{ *memory.Adapter }

func (uncertainSender) Send(context.Context, domain.Message) (domain.MessageID, error) {
	return "", domain.NewSendUncertain("WAID-U", errors.New("ack timeout"))
}

// A send that may have been delivered keeps its type through the
// dispatcher (the job and the wire map it) and is audited as such, not as
// a plain "error" (F4).
func TestSend_UncertainSurvivesTheDispatcherAndIsAudited(t *testing.T) {
	adapter := memory.New(nil)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: uncertainSender{adapter}, Events: adapter, Contacts: adapter, Groups: adapter,
		Session: adapter, Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter,
		Profile: "default", SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	_, err := d.Handle(context.Background(), "send", json.RawMessage(`{"to":"`+testJIDStr+`","body":"hi"}`))
	var u *domain.SendUncertainError
	if !errors.Is(err, domain.ErrSendUncertain) || !errors.As(err, &u) || u.MessageID != "WAID-U" {
		t.Fatalf("send err = %v, want ErrSendUncertain carrying WAID-U", err)
	}
	var last domain.AuditEvent
	for _, e := range adapter.AuditEntries() {
		last = e
	}
	if last.Detail != "send_uncertain" {
		t.Errorf("audit detail %q, want send_uncertain", last.Detail)
	}
}
