package whatsmeow

import (
	"context"
	"errors"
	"strings"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// sendWireBudget bounds a message send once nobody can cancel it any more.
// Package var so a test can shrink it.
var sendWireBudget = 90 * time.Second

// sendOnWire sends msg detached from ctx's cancellation: a hang-up,
// jobs.cancel or revocation must not abort a frame halfway, because the
// caller then cannot tell whether the message went out. The call keeps a
// bounded budget instead (the library's own ack timer is 75 s). If the ack
// wait ends without an answer, the frame may be on the wire, so the result
// is domain.ErrSendUncertain carrying the id — never a plain failure.
func sendOnWire(ctx context.Context, c whatsmeowClient, to waTypes.JID, msg *waE2E.Message) (waClient.SendResponse, error) {
	wctx, cancel := context.WithTimeout(sentCtx(ctx), sendWireBudget)
	defer cancel()
	resp, err := c.SendMessage(wctx, to, msg)
	if err != nil && (errors.Is(err, waClient.ErrMessageTimedOut) || errors.Is(err, context.DeadlineExceeded) || wroteFrame(resp, err)) {
		err = domain.NewSendUncertain(domain.MessageID(resp.ID), err)
	}
	return resp, err
}

// wroteFrame reports whether err came after whatsmeow wrote the message frame
// (go.mau.fi/whatsmeow send.go SendMessage). DebugTimings.Send is set once the
// write was attempted; the library wraps the write's own failure as "failed to
// send message node", so every other error then is an ack-wait outcome, and the
// frame is already out. Those are, with the library's file:line:
//   - ErrMessageTimedOut (send.go:432) and ctx.Err() (send.go:437), the ack wait;
//   - *DisconnectedError "message send" (request.go:195, :201, :232), from
//     retryFrame only, after the first write;
//   - ErrNotConnected (request.go:208) and the retry's SendFrame error
//     (request.go:216), the re-send after a reconnect;
//   - ErrIQTimedOut (request.go:226) and ctx.Err() (request.go:224).
//
// ErrServerReturnedError (send.go:454) is the server's refusal: a definite
// failure. A DisconnectedError for another action ("info query") is a
// pre-write prekey/device lookup.
func wroteFrame(resp waClient.SendResponse, err error) bool {
	var de *waClient.DisconnectedError
	if errors.As(err, &de) && strings.HasPrefix(de.Action, "message send") {
		return true
	}
	if resp.DebugTimings.Send == 0 || errors.Is(err, waClient.ErrServerReturnedError) {
		return false
	}
	return !strings.HasPrefix(err.Error(), "failed to send message node")
}

// sentCtx is the context for work after the frame went out (persisting the
// row): a cancel that arrived meanwhile must not lose it.
func sentCtx(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

// sendDecision is the audit decision for a failed send call.
func sendDecision(err error) string {
	if errors.Is(err, domain.ErrSendUncertain) {
		return "uncertain"
	}
	return "error"
}
