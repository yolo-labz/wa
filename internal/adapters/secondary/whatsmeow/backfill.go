package whatsmeow

import (
	"context"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Spec 110g — soft-stale BACKFILL extension.
//
// The soft-stale watchdog's recover action (WA_SOFT_STALE_RECOVER) forces
// a fresh websocket handshake when whatsmeow's keepalive still answers
// PINGs but no inbound traffic has flowed past the threshold — the
// "zombie link" signature. Reconnecting re-opens the socket, but it does
// NOT recover the messages that arrived during the stall: WhatsApp had
// already "delivered" them to the dead socket (server-acked) and does not
// redeliver on reconnect, and a linked/companion device gets no passive
// backfill. The only path that recovers them is an explicit on-demand
// history pull — exactly what ForceHistorySync's global mode does
// (BuildHistorySyncRequest(nil, count) = "the newest count messages
// globally", per sync.go). This file wires that pull onto the recover
// path. Incident: wa-burocracy lost inbound voice notes during a ~22min
// zombie stall (09/06/2026); the reconnect alone left the gap.

const (
	// backfillLoginWaitDefault bounds the wait for the post-reconnect
	// login to settle before BackfillRecent issues its pull. Reconnect
	// (Disconnect+Connect) returns once the socket is dialled, but login
	// completes asynchronously a beat later; firing the pull before
	// IsLoggedIn() is true would hit ForceHistorySync's ErrDisconnected
	// guard and silently lose the recovery.
	backfillLoginWaitDefault = 15 * time.Second
	// backfillPollEveryDefault is the login-state poll interval — short
	// enough to fire the pull promptly once login lands, long enough not
	// to spin.
	backfillPollEveryDefault = 250 * time.Millisecond
)

// BackfillRecent recovers messages missed during a soft-stale (zombie
// link) window. It is the action the cmd/wad watchdog runs AFTER a
// successful recover reconnect (spec 110g backfill extension, opt-in via
// WA_SOFT_STALE_BACKFILL). It waits — bounded — for the fresh login to
// settle, then issues a GLOBAL on-demand history pull for the newest
// historyRoundTripCap messages across all chats. The pull is persisted by
// the background history sync worker (persist-late), so the DB refreshes
// asynchronously; this returns once the request is fired, or a typed
// ErrDisconnected if login never settled.
//
// Global (zero-JID) scope is deliberate: a stall can miss messages in any
// number of chats and the daemon cannot know which, so "newest N
// globally" is the correct recovery direction. For a deeper single-chat
// pull an operator still has `wa sync <chat>`.
func (a *Adapter) BackfillRecent(ctx context.Context) (ForceSyncResult, error) {
	if err := a.waitLoggedIn(ctx); err != nil {
		return ForceSyncResult{}, err
	}
	return a.ForceHistorySync(ctx, domain.JID{}, historyRoundTripCap)
}

// waitLoggedIn blocks until the client reports connected AND logged-in, or
// returns a typed error on deadline / cancellation / shutdown. It bridges
// the async gap between Reconnect returning and login completing. It polls
// rather than subscribing to events because the adapter's event fan-out is
// already committed to the bridge; a short bounded poll keeps this
// self-contained and leak-free by construction.
func (a *Adapter) waitLoggedIn(ctx context.Context) error {
	if a.closed.Load() || a.client == nil {
		return fmt.Errorf("BackfillRecent: %w", domain.ErrDisconnected)
	}
	if a.client.IsConnected() && a.client.IsLoggedIn() {
		return nil
	}
	wait := a.backfillLoginWait
	if wait <= 0 {
		wait = backfillLoginWaitDefault
	}
	every := a.backfillPollEvery
	if every <= 0 {
		every = backfillPollEveryDefault
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.clientCtx.Done():
			return fmt.Errorf("BackfillRecent: %w", domain.ErrDisconnected)
		case <-deadline.C:
			return fmt.Errorf("BackfillRecent: %w: login not restored within %s", domain.ErrDisconnected, wait)
		case <-tick.C:
			if a.client.IsConnected() && a.client.IsLoggedIn() {
				return nil
			}
		}
	}
}
