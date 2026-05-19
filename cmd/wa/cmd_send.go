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
	sendListRowID      string
	sendListRowTitle   string
	sendButtonID       string
	sendTemplateButton string
	sendButtonDisplay  string
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
	switch {
	case sendListRowID != "":
		params := map[string]any{
			"to":    sendTo,
			"rowId": sendListRowID,
		}
		if sendListRowTitle != "" {
			params["title"] = sendListRowTitle
		}
		return "send.listResponse", params, nil
	case sendButtonID != "":
		params := map[string]any{
			"to":       sendTo,
			"buttonId": sendButtonID,
			"kind":     "button",
		}
		if sendButtonDisplay != "" {
			params["displayText"] = sendButtonDisplay
		}
		return "send.buttonResponse", params, nil
	case sendTemplateButton != "":
		params := map[string]any{
			"to":       sendTo,
			"buttonId": sendTemplateButton,
			"kind":     "templateButton",
		}
		if sendButtonDisplay != "" {
			params["displayText"] = sendButtonDisplay
		}
		return "send.buttonResponse", params, nil
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
}
