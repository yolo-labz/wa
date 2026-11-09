package testscript

import (
	"os"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/testtmp"
)

func TestMain(m *testing.M) {
	// Long $TMPDIR values overflow sun_path in unix-socket tests.
	cleanup := testtmp.Shorten()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
