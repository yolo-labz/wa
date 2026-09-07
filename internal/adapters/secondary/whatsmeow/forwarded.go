package whatsmeow

import (
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
)

// forwardInfo reads WhatsApp's forwarding markers off an inbound message.
//
// The markers live on ContextInfo — `isForwarded` (field 22) and
// `forwardingScore` (field 21) — and ContextInfo hangs off the individual
// message variant, not off waE2E.Message. There is no generic accessor:
// waE2E.Message.GetMessageContextInfo() is a different type carrying device
// metadata, so reading it here would return nothing and read as "never
// forwarded" for every message. Hence the variant walk, which mirrors the
// one in messageVariant.
//
// Score is what distinguishes "someone forwarded this once" from WhatsApp's
// own "forwarded many times" (score >= 5) chain-mail marker, so it is
// carried as the number rather than flattened into the bool.
//
// A plain Conversation cannot hold ContextInfo at all, so a bare text
// message is never marked forwarded — that is a property of the wire format,
// not a gap here.
func forwardInfo(msg *waE2E.Message) (bool, uint32) {
	ci := inboundContextInfo(msg)
	if ci == nil {
		return false, 0
	}
	return ci.GetIsForwarded(), ci.GetForwardingScore()
}

// inboundContextInfo returns the ContextInfo of whichever variant is
// populated, or nil. Ordered like messageVariant so the two stay readable
// side by side; every branch is nil-safe through the generated getters.
func inboundContextInfo(msg *waE2E.Message) *waE2E.ContextInfo {
	if msg == nil {
		return nil
	}
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage().GetContextInfo()
	case msg.GetContactMessage() != nil:
		return msg.GetContactMessage().GetContextInfo()
	case msg.GetLocationMessage() != nil:
		return msg.GetLocationMessage().GetContextInfo()
	default:
		return nil
	}
}
