package whatsmeow

import (
	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/yolo-labz/wa/internal/domain"
)

// extractInteractive maps the list/button reply fields of a whatsmeow
// *waE2E.Message into a domain.InteractivePayload per FR-130. Returns nil
// when the proto carries no recognised interactive response. The function
// is pure and safe to call with a nil msg.
//
// We intentionally do NOT translate outbound interactive messages
// (ListMessage, ButtonsMessage) — FR-131 makes sending them out of scope
// for v0.5. Only inbound replies materialise a domain payload.
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
