package sqlitehistory

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
)

// TestIdempotencySidecarContract runs the shared porttest suite against the
// sqlite-backed sidecar. Covers both the legacy Check/Record/Sweep surface
// (IS1..IS6) and the FR-034a LoadOrStore clauses (IS7..IS10).
func TestIdempotencySidecarContract(t *testing.T) {
	porttest.RunIdempotencyStoreContract(t, func(t *testing.T) app.IdempotencyStore {
		db := openV4SidecarDB(t)
		return NewIdempotencySidecar(db, nil)
	})
}
