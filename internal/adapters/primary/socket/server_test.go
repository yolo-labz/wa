package socket

import (
	"os"
	"testing"

	"go.uber.org/goleak"

	"github.com/yolo-labz/wa/v2/internal/testtmp"
)

func TestMain(m *testing.M) {
	// Long $TMPDIR values overflow sun_path in unix-socket tests.
	cleanup := testtmp.Shorten()
	goleak.VerifyTestMain(m, goleak.Cleanup(func(code int) {
		cleanup()
		os.Exit(code)
	}))
}
