package sqlitehistory_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitehistory"
)

// seedStore creates a Store with n messages across numChats chats.
func seedStore(b *testing.B, n, numChats int) *sqlitehistory.Store {
	b.Helper()
	dir := b.TempDir()
	s, err := sqlitehistory.Open(context.Background(), filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })

	const batchSize = 500
	for i := 0; i < n; i += batchSize {
		end := i + batchSize
		if end > n {
			end = n
		}
		msgs := make([]sqlitehistory.StoredMessage, 0, end-i)
		for j := i; j < end; j++ {
			msgs = append(msgs, sqlitehistory.StoredMessage{
				ChatJID:   fmt.Sprintf("chat%d@s.whatsapp.net", j%numChats),
				SenderJID: fmt.Sprintf("sender%d@s.whatsapp.net", j%100),
				MessageID: fmt.Sprintf("msg-%d", j),
				Timestamp: int64(1700000000 + j),
				Body:      fmt.Sprintf("Message body number %d for testing search and history queries", j),
			})
		}
		if err := s.Insert(context.Background(), msgs); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

// BenchmarkQueryHistory measures QueryHistory on a 10K-row database.
// SC-003: must be under 500ms.
func BenchmarkQueryHistory(b *testing.B) {
	if os.Getenv("WA_BENCH") == "" {
		b.Skip("set WA_BENCH=1 to run storage benchmarks")
	}
	s := seedStore(b, 10_000, 50)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		_, err := s.QueryHistory(ctx, "chat0@s.whatsapp.net", "", 20)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQuerySearch measures FTS5 search on a 10K-row database.
// SC-004: must be under 200ms.
func BenchmarkQuerySearch(b *testing.B) {
	if os.Getenv("WA_BENCH") == "" {
		b.Skip("set WA_BENCH=1 to run storage benchmarks")
	}
	s := seedStore(b, 10_000, 50)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		_, err := s.QuerySearch(ctx, "testing", 10)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsertBatch measures batch insert of 500 messages.
func BenchmarkInsertBatch(b *testing.B) {
	if os.Getenv("WA_BENCH") == "" {
		b.Skip("set WA_BENCH=1 to run storage benchmarks")
	}
	dir := b.TempDir()
	s, err := sqlitehistory.Open(context.Background(), filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })

	msgs := make([]sqlitehistory.StoredMessage, 500)
	for i := range msgs {
		msgs[i] = sqlitehistory.StoredMessage{
			ChatJID:   "bench@s.whatsapp.net",
			SenderJID: "sender@s.whatsapp.net",
			MessageID: fmt.Sprintf("batch-%d-%%d", i),
			Timestamp: int64(1700000000 + i),
			Body:      fmt.Sprintf("Batch message %d", i),
		}
	}

	b.ResetTimer()
	for n := range b.N {
		// Make message IDs unique per iteration.
		for i := range msgs {
			msgs[i].MessageID = fmt.Sprintf("batch-%d-%d", n, i)
		}
		if err := s.Insert(context.Background(), msgs); err != nil {
			b.Fatal(err)
		}
	}
}
