package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	sendTo             string
	sendBody           string
	sendIdempotencyKey string
	// Spec 110j: reply-class interactive flags. Each mutually exclusive
	// with --body and with each other.
	sendListRowID        string
	sendListRowTitle     string
	sendButtonID         string
	sendTemplateButton   string
	sendButtonDisplay    string
	sendContextStanzaID  string
	sendContextSenderJID string
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a text message",
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendTo == "" {
			return exitf(64, "wa send: --to is required")
		}

		modes := pickedSendModes()
		if len(modes) > 1 {
			return exitf(64, "wa send: %s are mutually exclusive", strings.Join(modes, ", "))
		}
		if len(modes) == 0 {
			return exitf(64, "wa send: one of --body, --list-row-id, --button-id, --template-button-id is required")
		}

		// #161: reply-class interactive sends MUST quote the original via
		// ContextInfo — the WhatsApp wire rejects an unquoted response
		// with server error 479. Enforce at the CLI boundary so the
		// operator sees a fast, actionable error before the daemon hop.
		if sendListRowID != "" || sendButtonID != "" || sendTemplateButton != "" {
			if sendContextStanzaID == "" {
				return exitf(64, "wa send: --context-stanza-id is required with --list-row-id / --button-id / --template-button-id (the message-ID of the inbound interactive being replied to)")
			}
		}

		method, params, err := buildSendParams()
		if err != nil {
			return err
		}
		if sendIdempotencyKey != "" {
			params["idempotencyKey"] = sendIdempotencyKey
		}

		result, exitCode, err := callAndClose(flagSocket, method, params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult(method, result, flagJSON))
		return nil
	},
}

// pickedSendModes returns the list of mutually-exclusive flags that were set,
// so the conflict message can name them precisely.
func pickedSendModes() []string {
	var picked []string
	if sendBody != "" {
		picked = append(picked, "--body")
	}
	if sendListRowID != "" {
		picked = append(picked, "--list-row-id")
	}
	if sendButtonID != "" {
		picked = append(picked, "--button-id")
	}
	if sendTemplateButton != "" {
		picked = append(picked, "--template-button-id")
	}
	return picked
}

// buildSendParams returns the JSON-RPC method + params for the picked send
// mode. Caller has already verified exactly one mode is set.
func buildSendParams() (string, map[string]any, error) {
	// withInteractiveContext stamps the #161 context fields onto a
	// reply-class params map. Caller has already validated that
	// sendContextStanzaID is non-empty.
	withInteractiveContext := func(p map[string]any) map[string]any {
		p["contextStanzaId"] = sendContextStanzaID
		if sendContextSenderJID != "" {
			p["contextSender"] = sendContextSenderJID
		}
		return p
	}
	switch {
	case sendListRowID != "":
		params := map[string]any{
			"to":    sendTo,
			"rowId": sendListRowID,
		}
		if sendListRowTitle != "" {
			params["title"] = sendListRowTitle
		}
		return "send.listResponse", withInteractiveContext(params), nil
	case sendButtonID != "":
		params := map[string]any{
			"to":       sendTo,
			"buttonId": sendButtonID,
			"kind":     "button",
		}
		if sendButtonDisplay != "" {
			params["displayText"] = sendButtonDisplay
		}
		return "send.buttonResponse", withInteractiveContext(params), nil
	case sendTemplateButton != "":
		params := map[string]any{
			"to":       sendTo,
			"buttonId": sendTemplateButton,
			"kind":     "templateButton",
		}
		if sendButtonDisplay != "" {
			params["displayText"] = sendButtonDisplay
		}
		return "send.buttonResponse", withInteractiveContext(params), nil
	default:
		return "send", map[string]any{
			"to":   sendTo,
			"body": sendBody,
		}, nil
	}
}

func init() {
	sendCmd.Flags().StringVar(&sendTo, "to", "", "recipient JID (e.g. 5511999999999@s.whatsapp.net)")
	sendCmd.Flags().StringVar(&sendBody, "body", "", "message text")
	sendCmd.Flags().StringVar(&sendIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	// Spec 110j: reply-class interactive flags.
	sendCmd.Flags().StringVar(&sendListRowID, "list-row-id", "", "reply to a peer ListMessage with this SelectedRowID (spec 110j)")
	sendCmd.Flags().StringVar(&sendListRowTitle, "list-row-title", "", "optional human-readable label echoed alongside --list-row-id")
	sendCmd.Flags().StringVar(&sendButtonID, "button-id", "", "reply to a peer ButtonsMessage with this SelectedButtonID (spec 110j)")
	sendCmd.Flags().StringVar(&sendTemplateButton, "template-button-id", "", "reply to a peer TemplateMessage with this SelectedID (spec 110j)")
	sendCmd.Flags().StringVar(&sendButtonDisplay, "button-display-text", "", "optional display label echoed alongside --button-id or --template-button-id")
	// #161: required wire-context for reply-class interactive sends.
	sendCmd.Flags().StringVar(&sendContextStanzaID, "context-stanza-id", "", "messageId of the inbound interactive being replied to (required with --list-row-id / --button-id / --template-button-id; #161)")
	sendCmd.Flags().StringVar(&sendContextSenderJID, "context-sender", "", "JID of the sender of the inbound interactive (defaults to --to for 1:1 chats; #161)")
}
