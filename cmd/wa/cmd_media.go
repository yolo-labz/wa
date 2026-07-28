package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// mediaFetchSocketFrameBudget bounds the inbound JSON-RPC response frame the
// CLI is willing to buffer while looping media.fetchBytes. It mirrors the
// daemon's socket frame cap (internal/adapters/primary/socket/framecap.go
// maxFrameBytes, 1 MiB): every chunk the daemon emits is clamped well under
// that, so a 1 MiB read buffer is always sufficient and never blocks on a
// legitimate response. The shared call() path uses a default 64 KiB
// bufio.Scanner that would choke on a base64-inflated media chunk, which is
// exactly why the socket fetch streams over its own reader instead.
const mediaFetchSocketFrameBudget = 1 << 20 // 1 MiB

// mediaFetchChunkBytes is the raw byte length the CLI requests per
// media.fetchBytes call. It matches the daemon's MediaFetchBytesChunkBytes
// (512 KiB) so each base64-inflated response (~683 KiB) sits comfortably
// under mediaFetchSocketFrameBudget. The daemon clamps the request to its own
// ceiling regardless, so this is a hint, not a trust boundary.
const mediaFetchChunkBytes = 512 * 1024

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

var (
	mediaListChat  string
	mediaListType  string
	mediaListLimit int
)

var mediaListCmd = &cobra.Command{
	Use:   "list",
	Short: "List media messages with cache status (sha256, size, duration)",
	Long: `List messages that carry media, newest first, with per-object cache
status. --chat narrows to one conversation; --media-type filters by kind
(audio|video|image|pdf|<mime>). Cached objects show their sha256 + on-disk
size; uncached rows show only the advertised metadata.

  wa media list --chat 5581...@s.whatsapp.net --media-type audio
  wa media list --limit 100 --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"limit": mediaListLimit}
		if mediaListChat != "" {
			body["chat"] = mediaListChat
		}
		if mediaListType != "" {
			body["mediaType"] = mediaListType
		}
		params, _ := json.Marshal(body)
		result, exitCode, err := callAndClose(flagSocket, "media.list", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			printNDJSONField("wa.media.list/v1", "media", result)
			return nil
		}
		printMediaTable(result)
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
		// Dry-run streams candidate rows as NDJSON so an operator can pipe
		// them to a filter before a real sweep, with a one-line reclaimable
		// summary on stderr (stdout stays a clean pipe). A real sweep returns
		// no per-file list, so it keeps the compact formatResult. (#180)
		if mediaDryRun && !flagJSON {
			printNDJSONField("wa.media.gc/v1", "files", result)
			printGCSummary(result)
			return nil
		}
		fmt.Println(formatResult("media.gc", result, flagJSON))
		return nil
	},
}

var mediaFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Download content-addressed media bytes to a file or stdout",
	Long: `Fetch the bytes of a cached media object by sha256 (or by message-id,
lazy-downloading it first). The on-disk path returned by 'media resolve' /
'media download' lives on the daemon host, not this client, so the bytes are
always streamed over the wire: over the local unix socket via chunked
media.fetchBytes, or against a --remote daemon via GET /media/<sha256>.

  wa media fetch --sha256 <hex> --out ./file.bin
  wa --remote https://host media fetch --message-id <id> --out ./video.mp4`,
	RunE: func(_ *cobra.Command, _ []string) error {
		if mediaSHA256 == "" && mediaMessageID == "" {
			return exitf(64, "wa media fetch: one of --sha256 or --message-id is required")
		}

		sha := mediaSHA256
		// A message-id without a sha: lazy-download so the payload is
		// cached daemon-side and we learn its content hash. We always need
		// the sha to address the bytes — over the socket via
		// media.fetchBytes, over --remote via GET /media/<sha>.
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
			sha = obj.SHA256
		}

		// Local socket mode: the daemon-side on-disk path is not reachable
		// here (the daemon runs in a container; the CLI is the SSH-forwarded
		// client), so stream the bytes over media.fetchBytes in frame-cap-
		// sized chunks instead of os.Open-ing a path that doesn't exist on
		// this host (audit #4).
		if flagRemote == "" {
			return fetchMediaSocket(flagSocket, sha)
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

// fetchMediaBytesResult mirrors the daemon's media.fetchBytes response. Bytes
// is a Go []byte, so encoding/json base64-decodes the wire string for us.
type fetchMediaBytesResult struct {
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Bytes  []byte `json:"bytes"`
	EOF    bool   `json:"eof"`
}

// fetchMediaSocket streams a cached media object's bytes over the local unix
// socket by looping media.fetchBytes (offset += len(bytes) until eof), writing
// to --out or stdout. The daemon chunks each response well under the 1 MiB
// frame cap, so the whole object — up to MaxInboundMediaBytes (50 MiB) — is
// reassembled here without ever buffering more than one chunk.
//
// A single connection is reused for the whole transfer: one system.hello
// handshake, then N fetchBytes frames. This avoids re-dialling per chunk and
// keeps the read side on a frame-cap-sized bufio.Reader (the shared call()
// path's default 64 KiB scanner cannot hold a base64-inflated chunk).
func fetchMediaSocket(socketPath, sha string) error {
	conn, err := dial(socketPath)
	if err != nil {
		return exiterr(exitUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	// One reader for the whole connection lifecycle (handshake + every
	// fetchBytes frame) so no response bytes are stranded between calls.
	br := bufio.NewReaderSize(conn, mediaFetchSocketFrameBudget)
	if err := sendHelloOn(conn, br); err != nil {
		return exiterr(exitGeneric, err)
	}

	out, closeOut, err := mediaOutWriter()
	if err != nil {
		return err
	}
	defer func() { _ = closeOut() }()

	// Read-timeout pattern: bound the whole transfer the same way the HTTP
	// path bounds its GET — 5 minutes is generous for a 50 MiB object over a
	// slow forwarded link without hanging forever.
	deadline := time.Now().Add(5 * time.Minute)
	if err := conn.SetDeadline(deadline); err != nil {
		return exiterr(exitGeneric, fmt.Errorf("set deadline: %w", err))
	}

	var offset int64
	for {
		params, _ := json.Marshal(map[string]any{
			"sha256": sha,
			"offset": offset,
			"length": mediaFetchChunkBytes,
		})
		res, rpcErr, callErr := callOn(conn, br, "media.fetchBytes", json.RawMessage(params))
		if callErr != nil {
			return exiterr(exitGeneric, callErr)
		}
		if rpcErr != nil {
			return exiterr(rpcCodeToExit(rpcErr.Code), rpcErr)
		}
		var chunk fetchMediaBytesResult
		if uerr := json.Unmarshal(res, &chunk); uerr != nil {
			return exiterr(exitGeneric, fmt.Errorf("parse media.fetchBytes: %w", uerr))
		}
		// Guard against an oversized object the daemon should have rejected,
		// and against a chunk that would push past the 50 MiB ceiling.
		if chunk.Size > domain.MaxInboundMediaBytes || offset+int64(len(chunk.Bytes)) > domain.MaxInboundMediaBytes {
			return exiterr(exitUsage, fmt.Errorf("media %s exceeds the %d-byte inbound cap", sha, domain.MaxInboundMediaBytes))
		}
		if len(chunk.Bytes) > 0 {
			if _, werr := out.Write(chunk.Bytes); werr != nil {
				return exiterr(exitGeneric, fmt.Errorf("write media: %w", werr))
			}
			offset += int64(len(chunk.Bytes))
		}
		if chunk.EOF {
			break
		}
		// Defensive: a non-EOF response that returned no bytes would loop
		// forever. The daemon never does this (it always advances or sets
		// eof), but fail loudly rather than spin.
		if len(chunk.Bytes) == 0 {
			return exiterr(exitGeneric, errors.New("media.fetchBytes: daemon returned an empty non-final chunk"))
		}
	}
	mediaWroteNotice()
	return nil
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

// mediaOutWriter opens the fetch output target: the operator-supplied --out
// file, or stdout when --out is unset. It returns the writer and a close
// function (a no-op for stdout); the caller streams to the writer and defers
// the close. Shared by the socket (chunk-loop) and HTTP (io.Copy) fetch paths
// so the target-selection logic lives in one place.
func mediaOutWriter() (io.Writer, func() error, error) {
	if mediaOut == "" {
		return os.Stdout, func() error { return nil }, nil
	}
	f, err := os.Create(mediaOut) //nolint:gosec // G304: mediaOut is the operator-supplied --out path
	if err != nil {
		return nil, nil, exiterr(exitGeneric, fmt.Errorf("create %s: %w", mediaOut, err))
	}
	return f, f.Close, nil
}

// mediaWroteNotice prints the human-mode "wrote <path>" confirmation to stderr
// after a successful --out fetch (stdout stays a clean byte pipe). No-op when
// streaming to stdout or in --json mode.
func mediaWroteNotice() {
	if mediaOut != "" && !flagJSON {
		fmt.Fprintln(os.Stderr, "wrote "+mediaOut)
	}
}

// writeMediaOut streams r to --out (or stdout when unset). Used by the --remote
// HTTP fetch path, which already holds an io.Reader over the response body.
func writeMediaOut(r io.Reader) error {
	w, closeOut, err := mediaOutWriter()
	if err != nil {
		return err
	}
	defer func() { _ = closeOut() }()
	if _, err := io.Copy(w, r); err != nil {
		return exiterr(exitGeneric, fmt.Errorf("write media: %w", err))
	}
	mediaWroteNotice()
	return nil
}

// printMediaTable renders media.list rows as a human table. Uncached rows
// leave SIZE/DUR/SHA256 blank since those come from the on-disk object.
func printMediaTable(result json.RawMessage) {
	var resp struct {
		Media []struct {
			Timestamp       int64  `json:"timestamp"`
			MediaType       string `json:"mediaType"`
			Caption         string `json:"caption"`
			Channel         string `json:"channel"`
			SHA256          string `json:"sha256"`
			Size            int64  `json:"size"`
			DurationSeconds int64  `json:"durationSeconds"`
			PTT             bool   `json:"ptt"`
			Cached          bool   `json:"cached"`
		} `json:"media"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		fmt.Println(formatResult("media.list", result, false))
		return
	}
	if len(resp.Media) == 0 {
		fmt.Println("No media")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TIME\tTYPE\tSIZE\tDUR\tCACHED\tSHA256\tCAPTION")
	for _, m := range resp.Media {
		ts := time.Unix(m.Timestamp, 0).Format("2006-01-02 15:04")
		dur := ""
		if m.DurationSeconds > 0 {
			dur = (time.Duration(m.DurationSeconds) * time.Second).String()
		}
		size := ""
		if m.Size > 0 {
			size = humanBytes(m.Size)
		}
		cached, sha := "no", "-"
		if m.Cached {
			cached = "yes"
			sha = m.SHA256
			if len(sha) > 12 {
				sha = sha[:12]
			}
		}
		// Inbound captions live (fenced) in the channel envelope; Caption is
		// empty for them. Show the envelope so the human still sees the text.
		capt := m.Caption
		if capt == "" && m.Channel != "" {
			capt = m.Channel
		}
		if len(capt) > 40 {
			capt = capt[:37] + "..."
		}
		// A voice note and an attached audio file share the audio/ogg MIME,
		// so the TYPE cell alone cannot tell them apart — tag the recorded
		// ones rather than spending a whole column on one boolean.
		mediaType := m.MediaType
		if m.PTT {
			mediaType += " (voice)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", ts, mediaType, size, dur, cached, sha, capt)
	}
	_ = w.Flush()
}

// printGCSummary writes a one-line dry-run summary to stderr: candidate count
// and the total reclaimable bytes summed from the per-file list.
func printGCSummary(result json.RawMessage) {
	var resp struct {
		Candidates int `json:"candidates"`
		Files      []struct {
			Size int64 `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return
	}
	var total int64
	for _, f := range resp.Files {
		total += f.Size
	}
	fmt.Fprintf(os.Stderr, "dry-run: %d candidate(s), %s reclaimable\n", resp.Candidates, humanBytes(total))
}

// humanBytes renders a byte count in IEC units (1024-based) for table display.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
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

	mediaListCmd.Flags().StringVar(&mediaListChat, "chat", "", "filter by chat JID")
	mediaListCmd.Flags().StringVar(&mediaListType, "media-type", "", "filter by media type (audio|video|image|pdf|<mime>)")
	mediaListCmd.Flags().IntVar(&mediaListLimit, "limit", 50, "max media rows (≤500)")

	mediaCmd.AddCommand(mediaListCmd)
	mediaCmd.AddCommand(mediaResolveCmd)
	mediaCmd.AddCommand(mediaDownloadCmd)
	mediaCmd.AddCommand(mediaGCCmd)
	mediaCmd.AddCommand(mediaFetchCmd)
	rootCmd.AddCommand(mediaCmd)
}
