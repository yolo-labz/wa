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

// seenHistory wraps the memory adapter's HistoryStore with a scripted
// LatestIncomingFinder so the dispatcher's sendSeen path is testable
// without a real history database.
type seenHistory struct {
	app.HistoryStore
	id    domain.MessageID
	found bool
	err   error
}

func (h *seenHistory) LatestIncoming(context.Context, domain.JID) (domain.MessageID, bool, error) {
	return h.id, h.found, h.err
}

func newSendSeenDispatcher(t *testing.T, history app.HistoryStore) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        history,
		Pairer:         adapter,
		Quoted:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// sendSeen marks the chat read at the newest incoming message and
// reports which id anchored the receipt.
func TestSendSeenHappyPath(t *testing.T) {
	base := memory.New(nil)
	d, adapter := newSendSeenDispatcher(t, &seenHistory{HistoryStore: base, id: "IN-42", found: true})
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionRead)

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})
	result, err := d.Handle(context.Background(), "sendSeen", params)
	if err != nil {
		t.Fatalf("Handle(sendSeen): %v", err)
	}
	var res struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID != "IN-42" {
		t.Errorf("messageId = %q, want IN-42", res.MessageID)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 || entries[0].Decision != "ok" || entries[0].Detail != "IN-42" {
		t.Errorf("audit = %+v, want one ok entry anchored to IN-42", entries)
	}
}

// No incoming message on record is an explicit -32117, never a silent
// success — an agent must not believe a receipt was sent.
func TestSendSeenNoIncomingIsMessageNotFound(t *testing.T) {
	base := memory.New(nil)
	d, adapter := newSendSeenDispatcher(t, &seenHistory{HistoryStore: base, found: false})
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionRead)

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})
	_, err := d.Handle(context.Background(), "sendSeen", params)
	if !errors.Is(err, app.ErrMessageNotFound) {
		t.Fatalf("want ErrMessageNotFound, got %v", err)
	}
}

// A history store without the LatestIncomingFinder extension answers
// -32601, mirroring thread.get's ThreadReader probe.
func TestSendSeenUnsupportedHistory(t *testing.T) {
	base := memory.New(nil) // memory adapter: HistoryStore only, no finder
	d, adapter := newSendSeenDispatcher(t, base)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionRead)

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})
	_, err := d.Handle(context.Background(), "sendSeen", params)
	if !errors.Is(err, app.ErrMethodNotFound) {
		t.Fatalf("want ErrMethodNotFound, got %v", err)
	}
}

// sendSeen runs the same allowlist gate as markRead (ActionRead) and is
// refused before the history lookup for a non-allowlisted chat.
func TestSendSeenAllowlistGate(t *testing.T) {
	base := memory.New(nil)
	d, _ := newSendSeenDispatcher(t, &seenHistory{HistoryStore: base, id: "IN-1", found: true})

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr})
	_, err := d.Handle(context.Background(), "sendSeen", params)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("want ErrNotAllowlisted, got %v", err)
	}
}
