package sqlitehistory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/internal/domain"
)

func openTempStore(t *testing.T) *sqlitehistory.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAndCloseHappyPath(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	first, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	second, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open against existing schema: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestInsertAndLoadMore(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	chat := domain.MustJID("15551234567@s.whatsapp.net")

	msgs := []domain.Message{
		domain.TextMessage{Recipient: chat, Body: "one"},
		domain.TextMessage{Recipient: chat, Body: "two"},
		domain.TextMessage{Recipient: chat, Body: "three"},
	}
	if err := s.InsertDomainMessages(context.Background(), msgs); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.LoadMore(context.Background(), chat, "", 2)
	if err != nil {
		t.Fatalf("LoadMore: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 msgs; got %d", len(got))
	}
	// Newest-first ordering: most recently inserted is "three".
	if tm, ok := got[0].(domain.TextMessage); !ok || tm.Body != "three" {
		t.Errorf("want newest=three; got %#v", got[0])
	}
}

func TestLoadMoreEmptyIsNotError(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	got, err := s.LoadMore(context.Background(), chat, "", 10)
	if err != nil {
		t.Fatalf("LoadMore on empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty; got %d", len(got))
	}
}

func TestLoadMoreRejectsZeroJIDAndBadLimit(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	if _, err := s.LoadMore(context.Background(), domain.JID{}, "", 10); err == nil {
		t.Error("want error for zero JID; got nil")
	}
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	if _, err := s.LoadMore(context.Background(), chat, "", 0); err == nil {
		t.Error("want error for limit=0; got nil")
	}
}

func TestSearchHitAndMiss(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	msgs := []domain.Message{
		domain.TextMessage{Recipient: chat, Body: "hello world"},
		domain.TextMessage{Recipient: chat, Body: "lunch at noon"},
		domain.TextMessage{Recipient: chat, Body: "endereço novo"},
	}
	if err := s.InsertDomainMessages(context.Background(), msgs); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	hits, err := s.Search(context.Background(), "hello", 10)
	if err != nil {
		t.Fatalf("Search hello: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("hello: want 1 hit; got %d", len(hits))
	}

	miss, err := s.Search(context.Background(), "nothingmatches", 10)
	if err != nil {
		t.Fatalf("Search miss: %v", err)
	}
	if len(miss) != 0 {
		t.Errorf("miss: want 0 hits; got %d", len(miss))
	}

	// Diacritic insensitivity (unicode61 remove_diacritics 2): query
	// without the cedilla should still match the row containing "endereço".
	accent, err := s.Search(context.Background(), "endereco", 10)
	if err != nil {
		t.Fatalf("Search endereco: %v", err)
	}
	if len(accent) != 1 {
		t.Errorf("accent fold: want 1 hit; got %d", len(accent))
	}
}
