package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

var (
	sendMediaTo             string
	sendMediaPath           string
	sendMediaCaption        string
	sendMediaMime           string
	sendMediaIdempotencyKey string
)

var sendMediaCmd = &cobra.Command{
	Use:   "sendMedia",
	Short: "Send a media message (image, video, document, etc.)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendMediaTo == "" || sendMediaPath == "" {
			return exitf(64, "wa sendMedia: --to and --path are required")
		}

		params := map[string]any{"to": sendMediaTo}
		if sendMediaCaption != "" {
			params["caption"] = sendMediaCaption
		}
		if sendMediaMime != "" {
			params["mime"] = sendMediaMime
		}
		if sendMediaIdempotencyKey != "" {
			params["idempotencyKey"] = sendMediaIdempotencyKey
		}

		// Remote mode: --path names a file on the CLIENT, which the daemon
		// cannot see. Transparently upload the local bytes to the daemon's
		// content-addressed store (POST /media/upload), then send by the
		// returned sha256 instead of the path (spec 198). Local/socket mode
		// keeps the original daemon-filesystem path behaviour byte-for-byte.
		if flagRemote != "" {
			sha, err := uploadLocalForSend(sendMediaPath)
			if err != nil {
				return err
			}
			params["sha256"] = sha
		} else {
			params["path"] = sendMediaPath
		}

		result, exitCode, err := callAndClose(flagSocket, "sendMedia", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("sendMedia", result, flagJSON))
		return nil
	},
}

// readLocalMediaForUpload reads path off the client filesystem and enforces
// the daemon's 16 MiB ceiling client-side, so an oversize file fails with a
// clear usage error before a multi-megabyte POST is attempted. The path is the
// operator-supplied --path / positional arg, read with the operator's own
// privileges — the same trust posture as any CLI file argument.
func readLocalMediaForUpload(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, exiterr(64, fmt.Errorf("stat %s: %w", path, err))
	}
	if info.IsDir() {
		return nil, exitf(64, "wa: %s is a directory, not a file", path)
	}
	if info.Size() > domain.MaxMediaBytes {
		return nil, exitf(64, "wa: %s is %d bytes, exceeds the 16 MiB media ceiling", path, info.Size())
	}
	if info.Size() == 0 {
		return nil, exitf(64, "wa: %s is empty", path)
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: path is the operator-supplied CLI argument, read with the operator's own privileges
	if err != nil {
		return nil, exiterr(1, fmt.Errorf("read %s: %w", path, err))
	}
	return b, nil
}

// uploadLocalForSend uploads a client-local file to the --remote daemon's
// content-addressed store and returns the sha256 the daemon computed, for use
// as the sendMedia `sha256` param. Token comes from $WA_TOKEN only (never
// argv). Spec 198.
func uploadLocalForSend(path string) (string, error) {
	payload, err := readLocalMediaForUpload(path)
	if err != nil {
		return "", err
	}
	sha, _, exitCode, err := callRemoteUpload(flagRemote, os.Getenv("WA_TOKEN"), payload)
	if err != nil {
		return "", exiterr(exitCode, err)
	}
	return sha, nil
}

func init() {
	sendMediaCmd.Flags().StringVar(&sendMediaTo, "to", "", "recipient JID")
	sendMediaCmd.Flags().StringVar(&sendMediaPath, "path", "", "path to media file on daemon's filesystem")
	sendMediaCmd.Flags().StringVar(&sendMediaCaption, "caption", "", "optional caption")
	sendMediaCmd.Flags().StringVar(&sendMediaMime, "mime", "", "optional MIME type override")
	sendMediaCmd.Flags().StringVar(&sendMediaIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	// #194: accept --chat as a universal recipient alias for --to.
	applyChatAlias(sendMediaCmd, "to")
}
