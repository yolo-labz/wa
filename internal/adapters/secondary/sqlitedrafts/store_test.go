package sqlitedrafts_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitedrafts"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
)

func TestDraftsMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "drafts.db")
	for i := range 3 {
		s, err := sqlitedrafts.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

func TestDraftStoreContract(t *testing.T) {
	factory := func(t *testing.T) app.DraftStore {
		t.Helper()
		dir := t.TempDir()
		s, err := sqlitedrafts.Open(context.Background(), filepath.Join(dir, "drafts.db"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	porttest.RunDraftStoreContract(t, factory)
}
