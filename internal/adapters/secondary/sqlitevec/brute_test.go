package sqlitevec_test

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitevec"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
)

func TestBruteVectorIndexContract(t *testing.T) {
	const dim = 8
	porttest.RunVectorIndexContract(t, dim, func(t *testing.T) app.VectorIndex {
		idx, err := sqlitevec.NewBrute(dim)
		if err != nil {
			t.Fatalf("NewBrute: %v", err)
		}
		return idx
	})
}
