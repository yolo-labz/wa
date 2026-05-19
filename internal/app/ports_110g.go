package app

// WebsocketProbe reports the live websocket lifecycle state observed by
// the secondary adapter. Spec 110g — soft-stale watchdog.
//
// The port exists because the cmd/wad watchdog goroutine and the health
// RPC both need to distinguish two distinct silences:
//
//   - Websocket DOWN + no inbound events: hard disconnect. whatsmeow's
//     own reconnect loop owns recovery. The watchdog stays out of the way.
//   - Websocket UP + no inbound events past the soft-stale threshold:
//     silent stall. whatsmeow's keepalive thinks the link is fine but no
//     traffic is flowing. The watchdog emits a synthetic softStale signal
//     so operators see the gap.
//
// Returning a single bool is intentional. The port is a peek into a
// rapidly-changing flag; richer state (last transition timestamp, error
// counts) belongs in ConnectivityHealthEvent, not in a probe.
//
// Per CLAUDE.md rule 23, the port surface uses no infrastructure types —
// just a bool. The whatsmeow adapter implements it by reading the
// atomic.Bool updated from handleWAEvent on ConnectionEvent translation.
type WebsocketProbe interface {
	WebsocketConnected() bool
}
