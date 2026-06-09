package domain

import "time"

// MessageRef is a minimal reference to an already-stored message: the four
// facts whatsmeow's on-demand history request needs to anchor a pull
// (BuildHistorySyncRequest fills ChatJID / OldestMsgID / OldestMsgFromMe /
// OldestMsgTimestamp from exactly these). It is deliberately distinct from
// the sealed outbound Message type, which carries only a recipient + body
// and no server id/timestamp.
//
// An on-demand history pull fetches `count` messages immediately BEFORE the
// anchor, so the choice of anchor encodes intent:
//   - newest stored message → recover a recent gap (a stall window) by
//     re-pulling the most recent slice, which the local store dedups.
//   - oldest stored message → paginate further back (scroll-up).
type MessageRef struct {
	Chat      JID
	ID        MessageID
	FromMe    bool
	Timestamp time.Time
}
