package whatsmeow

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow/appstate"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ResyncAppState rebuilds one app-state collection from the server,
// discarding the local snapshot.
//
// It exists because a diverged snapshot is a dead end otherwise. Every
// app-state write — chat.archive / mute / pin / markUnread, labels.*, and
// message.revoke scope=self — sends a patch keyed on the LOCAL version.
// When that version stops matching the server's, the server answers 409
// conflict and whatsmeow's catch-up fails to verify the returned patches
// ("mismatching LTHash"). Every one of those methods then fails forever,
// and before this the only recovery was re-pairing the session, which is
// a one-way door for a daemon whose whole value is a warm pairing.
//
// Observed live on wa-personal 08/09/2026: after a burst of
// deleteMessageForMe mutations plus a restart, regular_high stuck at
// "failed to verify patch v424" and every revoke returned -32603.
//
// full=false is the cheap catch-up (fetch what we are missing); full=true
// is the repair, and the one that fixes a hash mismatch.
func (a *Adapter) ResyncAppState(ctx context.Context, name string, full bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.client == nil {
		return errors.New("whatsmeow.ResyncAppState: no client")
	}
	if !a.client.IsConnected() {
		return fmt.Errorf("whatsmeow.ResyncAppState: %w", domain.ErrDisconnected)
	}
	patch, err := parsePatchName(name)
	if err != nil {
		return err
	}
	// onlyIfNotSynced=false: the whole point is to re-fetch a collection
	// we HAVE synced, because what we have is wrong.
	if err := a.client.FetchAppState(ctx, patch, full, false); err != nil {
		return fmt.Errorf("whatsmeow.ResyncAppState(%s, full=%v): %w", name, full, err)
	}
	return nil
}

// AppStateCollections are the collection names ResyncAppState accepts.
// Sourced from appstate.AllPatchNames so a whatsmeow addition cannot
// leave this list quietly short.
func AppStateCollections() []string {
	out := make([]string, 0, len(appstate.AllPatchNames))
	for _, n := range appstate.AllPatchNames {
		out = append(out, string(n))
	}
	return out
}

// parsePatchName maps a wire string to a whatsmeow collection, refusing
// anything not in AllPatchNames rather than passing an arbitrary string
// to the server.
func parsePatchName(name string) (appstate.WAPatchName, error) {
	for _, n := range appstate.AllPatchNames {
		if string(n) == name {
			return n, nil
		}
	}
	return "", fmt.Errorf("unknown app-state collection %q (want one of %v)",
		name, AppStateCollections())
}
