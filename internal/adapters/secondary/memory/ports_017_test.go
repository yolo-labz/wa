package memory_test

import (
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/app/porttest"
)

func TestIdempotencyStoreContract(t *testing.T) {
	porttest.RunIdempotencyStoreContract(t, func(t *testing.T) app.IdempotencyStore {
		return memory.NewIdempotencyStore()
	})
}

func TestDraftStoreContract(t *testing.T) {
	porttest.RunDraftStoreContract(t, func(t *testing.T) app.DraftStore {
		return memory.NewDraftStore()
	})
}

func TestMediaStoreContract(t *testing.T) {
	porttest.RunMediaStoreContract(t, func(t *testing.T) app.MediaStore {
		s, err := memory.NewMediaStore(t.TempDir(), &memory.FakeClock{T: time.Unix(1_700_000_000, 0).UTC()})
		if err != nil {
			t.Fatalf("NewMediaStore: %v", err)
		}
		return s
	})
}

func TestTranscriberContract(t *testing.T) {
	porttest.RunTranscriberContract(t, func(t *testing.T) app.Transcriber {
		return memory.NewTranscriber()
	})
}

func TestEventBufferContract(t *testing.T) {
	porttest.RunEventBufferContract(t, func(t *testing.T) app.EventBuffer {
		return memory.NewEventBuffer(10_000)
	})
}

func TestEventBusContract(t *testing.T) {
	porttest.RunEventBusContract(t, func(t *testing.T) app.EventBus {
		return memory.NewEventBus()
	})
}

// MessageSender_SendReply contract (T2-04).
func TestMessageSender_SendReply(t *testing.T) {
	porttest.RunReplySenderContract(t, func(t *testing.T) app.ReplySender {
		return memory.New(nil)
	})
}

func TestLabelManagerContract(t *testing.T) {
	porttest.RunLabelManagerContract(t, func(t *testing.T) app.LabelManager {
		return memory.NewLabelManager(func() time.Time {
			return time.Unix(1_700_000_000, 0).UTC()
		})
	})
}
