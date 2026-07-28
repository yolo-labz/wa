package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

var (
	sendMediaTo             string
	sendMediaPath           string
	sendMediaSHA256         string
	sendMediaCaption        string
	sendMediaMime           string
	sendMediaFilename       string
	sendMediaHumanize       bool
	sendMediaIdempotencyKey string
	sendMediaPTT            bool
	sendMediaSeconds        int
)

var sendMediaCmd = &cobra.Command{
	Use:   "sendMedia",
	Short: "Send a media message (image, video, document, etc.)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Exactly one payload source: --path (a file) XOR --sha256 (an
		// already-staged object). Mirrors the daemon's MediaMessage
		// SourceValidate XOR and makes `wa push` → `wa sendMedia --sha256`
		// work as documented — the daemon's sendMediaParams has accepted a
		// `sha256` param since spec 198; only this flag was missing.
		if sendMediaTo == "" {
			return exitf(64, "wa sendMedia: --to is required")
		}
		switch {
		case sendMediaPath == "" && sendMediaSHA256 == "":
			return exitf(64, "wa sendMedia: one of --path or --sha256 is required")
		case sendMediaPath != "" && sendMediaSHA256 != "":
			return exitf(64, "wa sendMedia: --path and --sha256 are mutually exclusive")
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
		if sendMediaHumanize {
			params["humanize"] = true
		}
		if sendMediaFilename != "" {
			params["filename"] = sendMediaFilename
		}
		// --ptt without --mime would reach the daemon as the generic
		// octet-stream sentinel and come back -32602, because the daemon
		// gates the voice-note hints on a declared audio type rather than a
		// sniffed one. Catch it here so the operator gets the reason instead
		// of a bare error code.
		if (sendMediaPTT || sendMediaSeconds != 0) && !strings.HasPrefix(sendMediaMime, "audio/") {
			return exitf(64, "wa sendMedia: --ptt/--seconds require an audio --mime (e.g. --mime 'audio/ogg; codecs=opus')")
		}
		if sendMediaPTT {
			params["ptt"] = true
		}
		if sendMediaSeconds != 0 {
			params["seconds"] = sendMediaSeconds
		}

		switch {
		case sendMediaSHA256 != "":
			// Send by content hash of an object already in the daemon's
			// content-addressed store (e.g. from a prior `wa push`). No
			// upload — works in both --remote and local/socket mode.
			params["sha256"] = sendMediaSHA256
		case flagRemote != "":
			// Remote mode: --path names a file on the CLIENT, which the daemon
			// cannot see. Transparently upload the local bytes to the daemon's
			// content-addressed store (POST /media/upload), then send by the
			// returned sha256 instead of the path (spec 198).
			sha, err := uploadLocalForSend(sendMediaPath)
			if err != nil {
				return err
			}
			params["sha256"] = sha
			// The daemon never sees the client-local path, so the display
			// filename (extension included) must travel explicitly — without
			// it the recipient got a generic unopenable ".bin" document and
			// MIME resolution lost the extension signal.
			if sendMediaFilename == "" {
				params["filename"] = filepath.Base(sendMediaPath)
			}
		default:
			// Local/socket mode keeps the original daemon-filesystem path
			// behaviour byte-for-byte.
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
	sendMediaCmd.Flags().StringVar(&sendMediaPath, "path", "", "path to media file (daemon's filesystem in local mode; client-local in --remote mode, auto-uploaded). Mutually exclusive with --sha256")
	sendMediaCmd.Flags().StringVar(&sendMediaSHA256, "sha256", "", "64-hex content hash of an already-staged object (e.g. from `wa push`). Mutually exclusive with --path")
	sendMediaCmd.Flags().StringVar(&sendMediaCaption, "caption", "", "optional caption")
	sendMediaCmd.Flags().StringVar(&sendMediaMime, "mime", "", "optional MIME type override (default: detected from filename extension, then content)")
	sendMediaCmd.Flags().StringVar(&sendMediaFilename, "filename", "", "display filename for document sends (defaults to --path basename; recommended with --sha256)")
	sendMediaCmd.Flags().StringVar(&sendMediaIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	sendMediaCmd.Flags().BoolVar(&sendMediaPTT, "ptt", false, "send an audio payload as a push-to-talk voice note (playable waveform bubble) instead of a file row; requires an audio --mime, and only Opus-in-Ogg renders a waveform — wa does not transcode")
	sendMediaCmd.Flags().IntVar(&sendMediaSeconds, "seconds", 0, "voice-note duration hint shown before download; wa has no duration probe, so supply it from whatever produced the audio")
	sendMediaCmd.Flags().BoolVar(&sendMediaHumanize, "humanize", false, "typing indicator + jittered human-scale delay before send (roadmap 2.3); composes with the rate limiter, never replaces it")
	// #194: accept --chat as a universal recipient alias for --to.
	applyChatAlias(sendMediaCmd, "to")
}
