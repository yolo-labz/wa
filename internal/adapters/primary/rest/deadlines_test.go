package rest

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestMethodDeadlines_EveryMethodClassified: app.MethodDeadlines and
// MethodScopes have the same key set (fail closed both ways), every
// entry has a class and a worker lifetime, and the detaching methods
// FR-116-16 names are job-capable.
func TestMethodDeadlines_EveryMethodClassified(t *testing.T) {
	t.Parallel()
	for method := range MethodScopes {
		d, ok := app.MethodDeadlines[method]
		switch {
		case !ok:
			t.Errorf("%s has a scope but no deadline class", method)
		case d.Class != app.Bounded && d.Class != app.Job:
			t.Errorf("%s: class %d", method, d.Class)
		case d.MaxWorker <= 0:
			t.Errorf("%s: no worker lifetime", method)
		}
	}
	for method := range app.MethodDeadlines {
		if _, ok := MethodScopes[method]; !ok {
			t.Errorf("%s is classified but has no scope", method)
		}
	}
	for _, method := range []string{"pair", "sync.force", "media.download"} {
		if app.MethodDeadlines[method].Class != app.Job {
			t.Errorf("%s must be job-capable", method)
		}
	}
}
