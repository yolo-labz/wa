package socket

import (
	"fmt"
	"os"

	"github.com/rogpeppe/go-internal/lockedfile"
)

// Acquire obtains an exclusive lock on the sibling file path+".lock" using
// lockedfile.Mutex. On success it removes any stale socket file at path
// (which is safe because the lock proves no other daemon is listening) and
// returns a release function that the caller MUST invoke at shutdown.
//
// If another daemon already holds the lock, Acquire returns ErrAlreadyRunning.
// The .lock file is never removed — it is zero bytes and harmless; leaving it
// lets the next startup distinguish a clean exit (lock released) from a crash.
//
// See research.md D8 and contracts/socket-path.md §Pre-flight check 6.
func Acquire(path string) (release func(), err error) {
	mu := &lockedfile.Mutex{Path: path + ".lock"}
	unlock, err := mu.Lock()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAlreadyRunning, err)
	}

	// Lock held — remove any stale socket file left by a crashed predecessor.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Cannot clean up stale socket; release the lock and fail.
		unlock()
		return nil, fmt.Errorf("socket: remove stale socket %s: %w", path, err)
	}

	return unlock, nil
}
