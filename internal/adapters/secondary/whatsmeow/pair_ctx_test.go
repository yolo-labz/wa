package whatsmeow

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"

	waClient "go.mau.fi/whatsmeow"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// ctxQRClient is the fake with whatsmeow's QR-channel contract
// (qrchan.go: when the ctx GetQRChannel got is done, the channel closes
// and the client disconnects), fed one item at a time through items.
type ctxQRClient struct {
	*fakeWhatsmeowClient
	items chan waClient.QRChannelItem
}

func (c *ctxQRClient) GetQRChannel(ctx context.Context) (<-chan waClient.QRChannelItem, error) {
	out := make(chan waClient.QRChannelItem)
	go func() {
		defer close(out)
		for {
			select {
			case it := <-c.items:
				out <- it
			case <-ctx.Done():
				c.Disconnect()
				return
			}
		}
	}()
	return out, nil
}

// stderrDuring returns what fn wrote to os.Stderr.
func stderrDuring(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&out, r); close(done) }()
	fn()
	os.Stderr = prev
	_ = w.Close()
	<-done
	return out.String()
}

// TestPair_CarriesSinkFromCtx is p8's S6a1 H1 probe
// (TestP8_PairCarriesSinkFromCtx) as a pass condition, through the public
// Pair: the ctx's QR sink gets every code; nothing reaches stderr (journald
// under systemd) or the temp pair page.
func TestPair_CarriesSinkFromCtx(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fake := newFakeClient()
	qr := make(chan waClient.QRChannelItem, 3)
	qr <- waClient.QRChannelItem{Event: "code", Code: "2@P4fixtureA,bm9pc2U="}
	qr <- waClient.QRChannelItem{Event: "code", Code: "2@P4fixtureB,bm9pc2U="}
	qr <- waClient.QRChannelItem{Event: "success"}
	close(qr)
	fake.QRChan = qr
	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	var got []string
	ctx := app.WithQRSink(context.Background(), func(code string) { got = append(got, code) })
	stderr := stderrDuring(t, func() {
		if err := a.Pair(ctx, ""); err != nil {
			t.Errorf("Pair: %v", err)
		}
	})
	entries, _ := os.ReadDir(os.TempDir())
	if len(got) != 2 || stderr != "" || len(entries) != 0 {
		t.Errorf("sink got %d codes; stderr %d bytes; temp entries %d; want 2, 0, 0", len(got), len(stderr), len(entries))
	}
}

// TestPair_CancelStopsPairing: cancelling Pair's ctx (the admin flow's
// cancel, or jobs.cancel, revocation or shutdown, which cancel the job
// ctx) reaches the ctx whatsmeow's QR channel holds, with a sink or on the
// CLI path: pairing ends at once instead of after the 3 min pairTimeout,
// the client disconnects, and a later rotation reaches no one.
func TestPair_CancelStopsPairing(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = null // the CLI path renders its QR there
	t.Cleanup(func() { os.Stderr = prev; _ = null.Close() })
	for _, withSink := range []bool{true, false} {
		synctest.Test(t, func(t *testing.T) {
			c := &ctxQRClient{fakeWhatsmeowClient: newFakeClient(), items: make(chan waClient.QRChannelItem, 1)}
			a := openWithClient(c, nil, nil, nil)
			defer func() { _ = a.Close() }()

			var codes []string
			ctx, cancel := context.WithCancel(context.Background())
			if withSink {
				ctx = app.WithQRSink(ctx, func(code string) { codes = append(codes, code) })
			}
			done := make(chan error, 1)
			go func() { done <- a.Pair(ctx, "") }()
			c.items <- waClient.QRChannelItem{Event: "code", Code: "2@P4fixtureA,bm9pc2U="}
			synctest.Wait()

			start := time.Now()
			cancel()
			err := <-done
			c.items <- waClient.QRChannelItem{Event: "code", Code: "2@P4fixtureB,bm9pc2U="}
			synctest.Wait()
			c.mu.Lock()
			disconnects := c.DisconnectCnt
			c.mu.Unlock()
			wantCodes := map[bool]int{true: 1, false: 0}[withSink]
			if err == nil || time.Since(start) != 0 || disconnects == 0 || len(codes) != wantCodes {
				t.Fatalf("sink %v, after cancel: err %v after %v, %d disconnects, sink codes %d; want an error at once, a disconnect, %d",
					withSink, err, time.Since(start), disconnects, len(codes), wantCodes)
			}
		})
	}
}
