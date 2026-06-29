package whatsmeow

import (
	waTypes "go.mau.fi/whatsmeow/types"
)

// announceUnavailable best-effort tells WhatsApp the device is offline so a
// 24/7 companion daemon does not surface the user as perpetually "online" to
// contacts. Fired on every Connected when presenceOffline is set (opt-in
// WA_PRESENCE_OFFLINE — see SetPresenceOffline). PR #280.
//
// Runs on its own goroutine: the whatsmeow event handler must return promptly
// (SynchronousAck=true) and SendPresence is a network round-trip. Errors are
// logged at debug and never fatal — WhatsApp rejects the presence until a
// push name is set, which is a harmless no-op on a fresh/unpaired session.
func (a *Adapter) announceUnavailable() {
	go func() {
		if err := a.client.SendPresence(a.clientCtx, waTypes.PresenceUnavailable); err != nil {
			if a.logger != nil {
				a.logger.Debug("presence-offline announce failed (non-fatal)", "error", err)
			}
		}
	}()
}
