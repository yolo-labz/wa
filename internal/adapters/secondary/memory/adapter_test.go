package memory_test

import (
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app/porttest"
)

func TestContractSuite(t *testing.T) {
	porttest.RunContractSuite(t, func(t *testing.T) porttest.Adapter {
		return memory.New(&memory.FakeClock{T: time.Unix(1_700_000_000, 0).UTC()})
	})
}
