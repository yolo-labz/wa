package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket/sockettest"
	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ctxForcer is a pull that runs until its ctx ends.
type ctxForcer struct{ started, ended chan struct{} }

func (f *ctxForcer) ForceSync(ctx context.Context, _ domain.JID, _ int, _ bool) (wmAdapter.ForceSyncResult, error) {
	close(f.started)
	<-ctx.Done()
	close(f.ended)
	return wmAdapter.ForceSyncResult{}, ctx.Err()
}

// TestSyncForce_ClientGoneInFirstWindowCancelsPull (WA-S4C2-FIX L1): the
// first call of `wa sync force` has no job id, so a client killed inside the
// 10 s window can only cancel through its connection. Over the real socket,
// closing it must end the pull at once, not leave it running to its deadline.
func TestSyncForce_ClientGoneInFirstWindowCancelsPull(t *testing.T) {
	f := &ctxForcer{started: make(chan struct{}), ended: make(chan struct{})}
	da, _ := jobsAdapter(t, nil, map[string]compositionHandler{"sync.force": makeSyncForceHandler(f, jobsAllow)})
	path := sockettest.TempSocketPath(t)
	server := socket.NewServer(da, slog.New(slog.DiscardHandler), socket.WithShutdownDeadline(2*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, path) }()
	t.Cleanup(func() { cancel(); <-done })
	sockettest.WaitForSocket(t, path, 3*time.Second)

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	hello := fmt.Sprintf(`{"jsonrpc":"2.0","id":0,"method":"system.hello","params":{"protoVersion":%d}}`+"\n", domain.ProtoVersion)
	if _, err := conn.Write([]byte(hello)); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"sync.force","params":{"chat":"15550000001@s.whatsapp.net"}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the pull never started")
	}
	_ = conn.Close() // the client is killed 1 s into the 10 s window
	select {
	case <-f.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the pull kept running after its client's connection closed")
	}
}
