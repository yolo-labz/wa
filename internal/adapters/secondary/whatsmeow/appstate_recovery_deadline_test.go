package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	waClient "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.uber.org/goleak"
)

// deadlineRecoveryClient holds the canceled send until the caller's
// watchdog has observed the deadline, making the outer abort path deterministic.
type deadlineRecoveryClient struct {
	*fakeWhatsmeowClient
	started chan struct{}
	unwind  chan struct{}
}

func (f *deadlineRecoveryClient) SendPeerMessage(ctx context.Context, _ *waE2E.Message) (waClient.SendResponse, error) {
	close(f.started)
	<-ctx.Done()
	<-f.unwind
	return waClient.SendResponse{}, ctx.Err()
}

func TestPeerRecoveryPreservesCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		fc.FetchAppStateErr = lthashFetchErr()
		seedVersions(fc)
		client := &deadlineRecoveryClient{
			fakeWhatsmeowClient: fc,
			started:             make(chan struct{}),
			unwind:              make(chan struct{}),
		}
		a := openWithClient(client, nil, discardLogger(), fixedNowFn)
		t.Cleanup(func() {
			_ = a.Close()
			goleak.VerifyNone(t, leakFreeGoleakOptions()...)
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- a.ResyncAppState(ctx, "regular_high", true) }()
		<-client.started
		<-ctx.Done()
		synctest.Wait() // outer abort selected; worker is still waiting to unwind
		close(client.unwind)

		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("caller deadline cause lost: got %v, want context.DeadlineExceeded", err)
		}
	})
}
