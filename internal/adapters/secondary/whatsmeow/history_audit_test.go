package whatsmeow

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// auditHistoryContainer is an in-test historyContainer double with a
// configurable InsertDomainMessages error. Used by the R-08 audit tests
// to distinguish the "emitted on success" path from the "suppressed on
// error" path without depending on the real sqlite store.
type auditHistoryContainer struct {
	insertErr error
	inserted  [][]domain.Message
}

func (s *auditHistoryContainer) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	return nil, nil
}

func (s *auditHistoryContainer) InsertDomainMessages(ctx context.Context, msgs []domain.Message) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.inserted = append(s.inserted, msgs)
	return nil
}

func (s *auditHistoryContainer) InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool, rawProto []byte) error {
	return nil
}

func (s *auditHistoryContainer) GetRawProto(ctx context.Context, messageID string) (string, []byte, error) {
	return "", nil, os.ErrNotExist
}

func (s *auditHistoryContainer) GetSender(ctx context.Context, messageID string) (string, error) {
	return "", os.ErrNotExist
}

func (s *auditHistoryContainer) Search(ctx context.Context, query string, limit int) ([]domain.Message, error) {
	return nil, nil
}
func (s *auditHistoryContainer) Close() error { return nil }

// deliverRemote races a goroutine to resolve the pending history
// request once LoadMore registers it. Retries briefly because LoadMore
// stores the pending entry just before blocking; a single attempt can
// win or lose depending on scheduling.
func deliverRemote(t *testing.T, a *Adapter, msgs []domain.Message) {
	t.Helper()
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if a.resolveHistoryReq(msgs) {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
}

// TestAuditHistoryCompleteOnNormalFinish: a successful remote
// BuildHistorySyncRequest round-trip MUST deposit an AuditHistoryComplete
// row with decision=ok.
func TestAuditHistoryCompleteOnNormalFinish(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	hist := &auditHistoryContainer{}
	a.history = hist
	t.Cleanup(func() { _ = a.Close() })

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	remote := []domain.Message{
		domain.TextMessage{Recipient: chat, Body: "remote-a"},
		domain.TextMessage{Recipient: chat, Body: "remote-b"},
	}
	deliverRemote(t, a, remote)

	got, err := a.LoadMore(context.Background(), chat, "", 10)
	if err != nil {
		t.Fatalf("LoadMore: %v", err)
	}
	if len(got) != len(remote) {
		t.Fatalf("want %d msgs, got %d", len(remote), len(got))
	}

	entries := a.auditBuf.Snapshot()
	var found bool
	for _, e := range entries {
		if e.Action == domain.AuditHistoryComplete && e.Decision == "ok" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("want AuditHistoryComplete[ok] row; got %d entries: %+v", len(entries), entries)
	}
}

// TestAuditHistoryCompleteNotEmittedOnError: when InsertDomainMessages
// fails, AuditPanic is recorded but AuditHistoryComplete MUST NOT be.
// The two actions are mutually exclusive so consumers can trust the
// signal.
func TestAuditHistoryCompleteNotEmittedOnError(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	insertErr := errors.New("disk full")
	a.history = &auditHistoryContainer{insertErr: insertErr}
	t.Cleanup(func() { _ = a.Close() })

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	remote := []domain.Message{
		domain.TextMessage{Recipient: chat, Body: "remote-a"},
	}
	deliverRemote(t, a, remote)

	if _, err := a.LoadMore(context.Background(), chat, "", 10); err != nil {
		t.Fatalf("LoadMore: %v", err)
	}

	entries := a.auditBuf.Snapshot()
	var complete, panicRow bool
	for _, e := range entries {
		if e.Action == domain.AuditHistoryComplete {
			complete = true
		}
		if e.Action == domain.AuditPanic && e.Decision == "history_insert" {
			panicRow = true
		}
	}
	if complete {
		t.Errorf("AuditHistoryComplete MUST NOT be emitted on insert error")
	}
	if !panicRow {
		t.Errorf("AuditPanic[history_insert] row expected on insert error")
	}
}
