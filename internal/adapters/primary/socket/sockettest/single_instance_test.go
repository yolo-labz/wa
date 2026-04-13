package sockettest

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// T037: second server returns ErrAlreadyRunning within 500ms.
func TestSingleInstance_SecondServerReturnsErrAlreadyRunning(t *testing.T) {
	_, path := startServer(t, nil)

	// Try to start a second server on the same path.
	fake2 := NewFakeDispatcher()
	srv2 := socket.NewServer(fake2, slog.Default())

	ctx2 := t.Context()

	start := time.Now()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv2.Run(ctx2, path)
	}()

	select {
	case err := <-errCh:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected error from second server, got nil")
		}
		if !errors.Is(err, socket.ErrAlreadyRunning) {
			t.Errorf("error = %v, want ErrAlreadyRunning", err)
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("second server took %v to fail, want < 500ms", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second server did not return within 5s")
	}
}

// T038: stale socket file with released lock is unlinked and replaced.
func TestSingleInstance_StaleSocketReplaced(t *testing.T) {
	path := TempSocketPath(t)

	// Create a fake stale socket file (no lock held).
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	_ = f.Close()

	// Verify the stale file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket should exist: %v", err)
	}

	// Start a server — it should acquire the lock, remove the stale file,
	// and listen successfully.
	fake := NewFakeDispatcher()
	srv := socket.NewServer(fake, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx, path)
	}()

	// Wait for the server to be listening.
	deadline := time.Now().Add(2 * time.Second)
	var connected bool
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			connected = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !connected {
		t.Fatal("server did not start listening after stale socket replacement")
	}

	t.Cleanup(func() {
		cancel()
		fake.Close()
		select {
		case err := <-errCh:
			if err != nil {
				t.Logf("server.Run returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not shut down within 5s")
		}
	})
}

// T039: stale socket file with held lock is NOT touched.
func TestSingleInstance_HeldLockBlocksNewServer(t *testing.T) {
	path := TempSocketPath(t)

	// Create a fake socket file.
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	_ = f.Close()

	// Acquire the lock manually (simulating a running daemon).
	release, err := socket.Acquire(path)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	// Note: Acquire removes the stale socket, so re-create it.
	f2, err := os.Create(path)
	if err != nil {
		t.Fatalf("re-create socket: %v", err)
	}
	_ = f2.Close()
	t.Cleanup(func() { release() })

	// Try to start a server — should fail with ErrAlreadyRunning.
	fake := NewFakeDispatcher()
	srv := socket.NewServer(fake, slog.Default())
	ctx := t.Context()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx, path)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from server with held lock, got nil")
		}
		if !errors.Is(err, socket.ErrAlreadyRunning) {
			t.Errorf("error = %v, want ErrAlreadyRunning", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not return within 5s")
	}

	// Assert the fake socket file still exists.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("socket file should still exist, got: %v", err)
	}
}

// T040: lock released on graceful shutdown allows immediate restart.
func TestSingleInstance_RestartAfterGracefulShutdown(t *testing.T) {
	path := TempSocketPath(t)

	// Start server 1.
	fake1 := NewFakeDispatcher()
	srv1 := socket.NewServer(fake1, slog.Default())
	ctx1, cancel1 := context.WithCancel(context.Background())

	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- srv1.Run(ctx1, path)
	}()

	// Wait for server 1 to be listening.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Shut down server 1.
	cancel1()
	fake1.Close()
	select {
	case err := <-errCh1:
		if err != nil {
			t.Logf("server1.Run returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server 1 did not shut down within 5s")
	}

	// Start server 2 immediately on the same path.
	fake2 := NewFakeDispatcher()
	srv2 := socket.NewServer(fake2, slog.Default())
	ctx2, cancel2 := context.WithCancel(context.Background())

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- srv2.Run(ctx2, path)
	}()

	// Wait for server 2 to be listening.
	deadline = time.Now().Add(2 * time.Second)
	var connected bool
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			connected = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !connected {
		t.Fatal("server 2 did not start listening after server 1 shutdown")
	}

	t.Cleanup(func() {
		cancel2()
		fake2.Close()
		select {
		case err := <-errCh2:
			if err != nil {
				t.Logf("server2.Run returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server 2 did not shut down within 5s")
		}
	})
}
