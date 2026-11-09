package app

import (
	"fmt"
	"reflect"
	"testing"
)

// Exercise all 2^7 reason sets, including unreachable combinations, to prove
// severity dominance and that either warning alone never drains readiness.
func TestHealthTruthTable_AllReasonCombinations(t *testing.T) {
	names := []string{"not_paired", "storage_unknown", "storage_unwritable", "disconnected", "ingest_stale", "soft_stale", "storage_low"}
	for mask := range 128 {
		t.Run(fmt.Sprintf("%07b", mask), func(t *testing.T) {
			var holds [7]bool
			var wantReasons, wantUnready []string
			wantHealth := "healthy"
			for i, name := range names {
				holds[i] = mask&(1<<i) != 0
				if !holds[i] {
					continue
				}
				wantReasons = append(wantReasons, name)
				if i < 5 {
					wantUnready = append(wantUnready, name)
				}
			}
			if mask != 0 {
				wantHealth = "degraded"
			}
			if mask&7 != 0 {
				wantHealth = "down"
			}
			health, reasons, unready := healthDecision(holds)
			if health != wantHealth || !reflect.DeepEqual(reasons, wantReasons) || !reflect.DeepEqual(unready, wantUnready) {
				t.Fatalf("decision=%s/%v/%v want=%s/%v/%v", health, reasons, unready, wantHealth, wantReasons, wantUnready)
			}
		})
	}
}
