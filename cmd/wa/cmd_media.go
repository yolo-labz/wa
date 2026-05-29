package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	mediaSHA256     string
	mediaMessageID  string
	mediaTranscribe bool
	mediaOlderSecs  int64
	mediaDryRun     bool
	mediaOut        string
)

var mediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Content-addressed media operations",
}

var mediaResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Return the on-disk path for a content-addressed sha256",
	RunE: func(cmd *cobra.Command, args []string) error {
		if mediaSHA256 == "" {
			return exitf(64, "wa media resolve: --sha256 is required")
		}
		params, _ := json.Marshal(map[string]any{"sha256": mediaSHA256})
		result, exitCode, err := callAndClose(flagSocket, "media.resolve", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("media.resolve", result, flagJSON))
		return nil
	},
}

var mediaDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Lazy-fetch the media payload for a messageId; prints the on-disk path",
	RunE: func(cmd *cobra.Command, args []string) error {
		if mediaMessageID == "" {
			return exitf(64, "wa media download: --message-id is required")
		}
		params, _ := json.Marshal(map[string]any{
			"messageId":  mediaMessageID,
			"transcribe": mediaTranscribe,
		})
		result, exitCode, err := callAndClose(flagSocket, "media.download", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatResult("media.download", result, true))
			return nil
		}
		// Print the on-disk path on stdout (FR-050a contract).
		var obj struct {
			Object struct {
				Path string `json:"path"`
			} `json:"object"`
		}
		_ = json.Unmarshal(result, &obj)
		fmt.Println(obj.Object.Path)
		return nil
	},
}

var mediaGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage-collect media older than a given window",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{
			"olderThanSeconds": mediaOlderSecs,
			"dryRun":           mediaDryRun,
		})
		result, exitCode, err := callAndClose(flagSocket, "media.gc", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("media.gc", result, flagJSON))
		return nil
	},
}

var mediaFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Download content-addressed media bytes to a file or stdout",
	Long: `Fetch the bytes of a cached media object by sha256 (or by message-id,
lazy-downloading it first). Against a --remote daemon the bytes are streamed
over the daemon's GET /media/<sha256> endpoint (the on-disk path returned by
'media resolve'/'media download' is on the daemon host, not reachable here).
Without --remote the local on-disk path is copied directly.

  wa media fetch --sha256 <hex> --out ./file.bin
  wa --remote https://host media fetch --message-id <id> --out ./video.mp4`,
	RunE: func(_ *cobra.Command, _ []string) error {
		if mediaSHA256 == "" && mediaMessageID == "" {
			return exitf(64, "wa media fetch: one of --sha256 or --message-id is required")
		}

		sha := mediaSHA256
		// A message-id without a sha: lazy-download so the payload is
		// cached daemon-side and we learn its content hash. In local
		// mode the returned path is reachable here, so copy it directly.
		if sha == "" {
			result, exitCode, err := callAndClose(flagSocket, "media.download", map[string]any{
				"messageId":  mediaMessageID,
				"transcribe": false,
			})
			if err != nil {
				return exiterr(exitCode, err)
			}
			obj, err := mediaObjectFrom(result)
			if err != nil {
				return exiterr(1, err)
			}
			if flagRemote == "" {
				return streamMediaFile(obj.Path)
			}
			sha = obj.SHA256
		}

		// Local mode with a sha: resolve to the on-disk path and copy it.
		if flagRemote == "" {
			result, exitCode, err := callAndClose(flagSocket, "media.resolve", map[string]any{"sha256": sha})
			if err != nil {
				return exiterr(exitCode, err)
			}
			obj, err := mediaObjectFrom(result)
			if err != nil {
				return exiterr(1, err)
			}
			return streamMediaFile(obj.Path)
		}

		// Remote mode: stream the bytes over the HTTP media endpoint.
		return fetchMediaHTTP(flagRemote, os.Getenv("WA_TOKEN"), sha)
	},
}

// mediaObjectView mirrors the daemon's on-wire media object shape — only
// the fields the fetch command needs.
type mediaObjectView struct {
	SHA256 string `json:"sha256"`
	Path   string `json:"path"`
}

func mediaObjectFrom(result json.RawMessage) (mediaObjectView, error) {
	var env struct {
		Object mediaObjectView `json:"object"`
	}
	if err := json.Unmarshal(result, &env); err != nil {
		return mediaObjectView{}, fmt.Errorf("parse media result: %w", err)
	}
	return env.Object, nil
}

// streamMediaFile copies a daemon-local file (local-socket mode) to --out
// or stdout.
func streamMediaFile(path string) error {
	if path == "" {
		return exitf(1, "wa media fetch: daemon returned an empty path")
	}
	f, err := os.Open(path) //nolint:gosec // G304: path comes from the trusted local daemon, not user argv
	if err != nil {
		return exiterr(1, fmt.Errorf("open media %s: %w", path, err))
	}
	defer func() { _ = f.Close() }()
	return writeMediaOut(f)
}

// fetchMediaHTTP GETs /media/<sha256> on a --remote daemon and writes the
// body to --out or stdout. Status mapping mirrors callRemote.
func fetchMediaHTTP(remoteURL, token, sha string) error {
	endpoint, err := mediaEndpointURL(remoteURL, sha)
	if err != nil {
		return exiterr(64, err)
	}
	// Media payloads cap at 50 MiB (domain.MaxInboundMediaBytes); 5 min
	// is generous for a slow link without hanging forever.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return exiterr(1, fmt.Errorf("build request: %w", err))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return exiterr(10, fmt.Errorf("fetch media: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusOK:
		// fall through to stream the body
	case resp.StatusCode == http.StatusUnauthorized:
		return exiterr(64, errors.New("remote auth failed: missing or invalid $WA_TOKEN; HTTP 401"))
	case resp.StatusCode == http.StatusNotFound:
		return exiterr(10, fmt.Errorf("media %s not cached on remote daemon (HTTP 404)", sha))
	case resp.StatusCode >= 500:
		return exiterr(70, fmt.Errorf("remote daemon returned HTTP %d", resp.StatusCode))
	default:
		return exiterr(64, fmt.Errorf("remote daemon returned HTTP %d", resp.StatusCode))
	}
	return writeMediaOut(resp.Body)
}

// mediaEndpointURL validates the remote URL via the same gate as the RPC
// transport, then swaps the path for /media/<sha256>.
func mediaEndpointURL(remoteURL, sha string) (string, error) {
	canonical, err := normaliseRemoteURL(remoteURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(canonical)
	if err != nil {
		return "", fmt.Errorf("parse remote URL: %w", err)
	}
	u.Path = "/media/" + sha
	return u.String(), nil
}

// writeMediaOut streams r to --out (or stdout when unset).
func writeMediaOut(r io.Reader) error {
	if mediaOut == "" {
		if _, err := io.Copy(os.Stdout, r); err != nil {
			return exiterr(1, fmt.Errorf("write stdout: %w", err))
		}
		return nil
	}
	f, err := os.Create(mediaOut) //nolint:gosec // G304: mediaOut is the operator-supplied --out path
	if err != nil {
		return exiterr(1, fmt.Errorf("create %s: %w", mediaOut, err))
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, r); err != nil {
		return exiterr(1, fmt.Errorf("write %s: %w", mediaOut, err))
	}
	if !flagJSON {
		fmt.Fprintln(os.Stderr, "wrote "+mediaOut)
	}
	return nil
}

func init() {
	mediaResolveCmd.Flags().StringVar(&mediaSHA256, "sha256", "", "64-hex content hash")
	mediaDownloadCmd.Flags().StringVar(&mediaMessageID, "message-id", "", "originating message id")
	mediaDownloadCmd.Flags().BoolVar(&mediaTranscribe, "transcribe", false, "transcribe voice notes")
	mediaGCCmd.Flags().Int64Var(&mediaOlderSecs, "older-than-seconds", 30*86400, "cutoff age")
	mediaGCCmd.Flags().BoolVar(&mediaDryRun, "dry-run", false, "candidate count without deletion")
	mediaFetchCmd.Flags().StringVar(&mediaSHA256, "sha256", "", "64-hex content hash")
	mediaFetchCmd.Flags().StringVar(&mediaMessageID, "message-id", "", "originating message id (lazy-downloaded first)")
	mediaFetchCmd.Flags().StringVar(&mediaOut, "out", "", "output file path (default: stdout)")

	mediaCmd.AddCommand(mediaResolveCmd)
	mediaCmd.AddCommand(mediaDownloadCmd)
	mediaCmd.AddCommand(mediaGCCmd)
	mediaCmd.AddCommand(mediaFetchCmd)
	rootCmd.AddCommand(mediaCmd)
}
