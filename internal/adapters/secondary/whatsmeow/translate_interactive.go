package whatsmeow

import (
	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// extractInteractive maps the list/button reply fields of a whatsmeow
// *waE2E.Message into a domain.InteractivePayload per FR-130. Returns nil
// when the proto carries no recognised interactive response. The function
// is pure and safe to call with a nil msg.
//
// We intentionally do NOT translate outbound *unsolicited* interactive
// messages (ListMessage, ButtonsMessage, InteractiveMessage, TemplateMessage
// with button definitions) — spec 110j splits FR-131 of spec 017: unsolicited
// menus stay forbidden (anti-spam), reply-class echoes (ListResponseMessage,
// ButtonsResponseMessage, TemplateButtonReplyMessage) flow through
// buildOutboundMessage. Only inbound replies materialise a domain payload here.
func extractInteractive(msg *waE2E.Message) *domain.InteractivePayload {
	if msg == nil {
		return nil
	}
	if lr := msg.GetListResponseMessage(); lr != nil {
		sel := lr.GetSingleSelectReply()
		if sel == nil {
			return nil
		}
		return &domain.InteractivePayload{
			Subtype: domain.InteractiveList,
			Prompt:  lr.GetTitle(),
			Options: []domain.InteractiveOption{{
				ID:    sel.GetSelectedRowID(),
				Label: lr.GetTitle(),
			}},
		}
	}
	if br := msg.GetButtonsResponseMessage(); br != nil {
		return &domain.InteractivePayload{
			Subtype: domain.InteractiveButtons,
			Prompt:  br.GetSelectedDisplayText(),
			Options: []domain.InteractiveOption{{
				ID:    br.GetSelectedButtonID(),
				Label: br.GetSelectedDisplayText(),
			}},
		}
	}
	if tb := msg.GetTemplateButtonReplyMessage(); tb != nil {
		return &domain.InteractivePayload{
			Subtype: domain.InteractiveButtons,
			Prompt:  tb.GetSelectedDisplayText(),
			Options: []domain.InteractiveOption{{
				ID:    tb.GetSelectedID(),
				Label: tb.GetSelectedDisplayText(),
			}},
		}
	}
	return nil
}
