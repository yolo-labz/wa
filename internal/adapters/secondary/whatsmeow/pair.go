package whatsmeow

// pair.go implements the Adapter.Pair flow for feature 003 commit 5
// (T033). Two flows are supported per FR-008:
//
//  1. QR-in-terminal (default; phone == ""): GetQRChannel + Connect, then
//     read QR codes from the channel and render each one to os.Stderr via
//     mdp/qrterminal/v3 GenerateHalfBlock until the upstream sends
//     {Event: "success"} or {Event: "timeout"}.
//
//  2. Phone-pairing-code (phone != ""): Connect + PairPhone, print the
//     8-character linking code to os.Stderr, then block on
//     a.pairSuccessCh until handleWAEvent surfaces events.PairSuccess
//     or the detached pairing context expires.
//
// Both flows allocate a fresh **detached** 3-minute context — NOT the
// caller's ctx, NOT a.clientCtx — per CLAUDE.md §"Daemon, IPC,
// single-instance" and the aldinokemal lesson cited in the same section.
// Reusing a request-scoped context here would cancel the QR emitter the
// instant the JSON-RPC handler returns.

import (
	"context"
	"fmt"
	"os"
	"time"

	waClient "go.mau.fi/whatsmeow"

	"github.com/mdp/qrterminal/v3"
)

func pairWrap(err error) error { return fmt.Errorf("pair: %w", err) }

// pairTimeout is the detached pairing window. Declared as a package
// variable rather than a constant so pair_test.go can shorten it for the
// timeout case (case 4 in T034). Production never mutates it.
var pairTimeout = 3 * time.Minute

// Pair runs the WhatsApp pairing flow. Behaviour is documented at the
// top of this file. Pair short-circuits with nil if the underlying
// whatsmeow client is already logged in (US2 acceptance scenario 3:
// existing-session reuse).
//
// The ctx parameter is honoured only for the early IsLoggedIn check; the
// pairing operation itself uses a fresh detached context.
func (a *Adapter) Pair(_ context.Context, phone string) error {
	if a.client.IsLoggedIn() {
		return nil
	}

	// Detached pairing context — see file header comment.
	pairCtx, pairCancel := context.WithTimeout(context.Background(), pairTimeout)
	defer pairCancel()

	if phone == "" {
		return a.pairQR(pairCtx)
	}
	return a.pairPhone(pairCtx, phone)
}

// pairQR runs the QR-in-terminal flow. GetQRChannel MUST be called
// BEFORE Connect, per the whatsmeow QR contract: the channel is created
// against the device's pre-pairing state and Connect kicks the websocket
// that produces the QR codes.
func (a *Adapter) pairQR(pairCtx context.Context) error {
	qrChan, err := a.client.GetQRChannel(pairCtx)
	if err != nil {
		return pairWrap(err)
	}
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("pair connect: %w", err)
	}
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			// Render the half-block QR to stderr (backwards compat).
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stderr) //nolint:forbidigo // user-facing pair UX
			// Also write an HTML file the client can open in a browser
			// via `wa pair --browser`. Best-effort; errors are logged
			// but do not abort the QR flow.
			if err := writeQRHTML(evt.Code, false); err != nil {
				a.logger.Warn("writeQRHTML failed", "error", err)
			}
		case "success":
			_ = writeQRHTML("", true)
			return nil
		case "timeout":
			return pairWrap(context.DeadlineExceeded)
		case "err-client-outdated", "unavailable":
			return pairWrap(fmt.Errorf("%s", evt.Event))
		}
	}
	return pairWrap(fmt.Errorf("qr channel closed without success"))
}

// pairPhone runs the phone-pairing-code flow. After Connect + PairPhone
// returns the 8-character linking code, the function blocks on
// a.pairSuccessCh which handleWAEvent signals when events.PairSuccess
// arrives over the websocket.
func (a *Adapter) pairPhone(pairCtx context.Context, phone string) error {
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("pair connect: %w", err)
	}
	code, err := a.client.PairPhone(pairCtx, phone, true, waClient.PairClientChrome, "wad")
	if err != nil {
		return fmt.Errorf("pair phone: %w", err)
	}
	// Print the linking code + instructions to stderr. Stderr is the
	// canonical channel for human-facing UX from the daemon (see
	// pairQR). //nolint:forbidigo because user-facing pair UX is the
	// one place the adapter is permitted to write to a tty.
	fmt.Fprintf(os.Stderr, "Linking code: %s\n", code)                                          //nolint:forbidigo // pair UX
	fmt.Fprintln(os.Stderr, "WhatsApp -> Settings -> Linked Devices -> Link with phone number") //nolint:forbidigo // pair UX

	select {
	case <-a.pairSuccessCh:
		return nil
	case <-pairCtx.Done():
		return pairWrap(pairCtx.Err())
	}
}
