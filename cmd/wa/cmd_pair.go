package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"golang.org/x/sys/unix"
)

var (
	pairPhone          string
	pairBrowser        bool
	pairIdempotencyKey string
	// pairRemote drives the SSH-chain re-pair path for daemons deployed
	// behind dokku. Value shape: `<ssh-host>:<dokku-app>`. When non-empty,
	// the cobra RunE short-circuits to runPairRemote (cmd_pair_remote.go)
	// instead of dialing the local unix socket. Feature 110e.
	pairRemote string
	// pairReset triggers a server-side single-device logout (whatsmeow
	// Client.Logout → Store.Delete) before the actual pair RPC, so an
	// operator can re-pair a daemon that is already linked WITHOUT
	// wiping messages.db / audit.log. Feature 110f. Distinct from
	// `wa panic` which is a full R-07 wipe.
	pairReset bool
)

// pairHTMLPath mirrors the daemon-side path (os.TempDir + wa-pair.html).
// Both processes run as the same user on the same host so they share
// the same tmp directory. Kept in sync with
// internal/adapters/secondary/whatsmeow/pair_html.go PairHTMLPath().
func pairHTMLPath() string {
	return filepath.Join(os.TempDir(), "wa-pair.html")
}

// writeLoadingHTML writes a placeholder HTML file so the browser has
// something to display before the first QR code arrives from the daemon.
func writeLoadingHTML(path string) error {
	const loading = `<!DOCTYPE html>
<html><head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="1">
<title>wa pair</title>
<style>
  html,body{height:100%;margin:0;background:#0a0a0a;color:#e5e5e5;
    font-family:-apple-system,system-ui,sans-serif;
    display:flex;align-items:center;justify-content:center;}
  .card{background:#141414;border:1px solid #262626;padding:2.5rem 3rem;
    border-radius:14px;text-align:center;max-width:420px;}
  h1{margin:0 0 0.5rem;font-size:1.15rem;font-weight:600;}
  .hint{font-size:0.875rem;color:#737373;margin-top:1rem;}
  .spinner{display:inline-block;width:32px;height:32px;border:3px solid #262626;
    border-top-color:#4ade80;border-radius:50%;animation:spin 0.8s linear infinite;margin:1rem;}
  @keyframes spin{to{transform:rotate(360deg);}}
</style>
</head><body><div class="card">
  <h1>Connecting to WhatsApp…</h1>
  <div class="spinner"></div>
  <p class="hint">The QR code will appear here in a moment.</p>
</div></body></html>`
	// SEC-03: refuse pre-planted symlinks in the shared tmp dir —
	// O_EXCL fails on ANY existing path; remove a stale file once and
	// retry. Mirrors openPairFile in the whatsmeow adapter.
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY | unix.O_NOFOLLOW
	f, err := os.OpenFile(path, flags, 0o600) //nolint:gosec // path derived from os.TempDir()
	if os.IsExist(err) {
		if rmErr := os.Remove(path); rmErr != nil {
			return rmErr
		}
		f, err = os.OpenFile(path, flags, 0o600) //nolint:gosec // see above
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(loading); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// openBrowser opens a file:// URL in the user's default browser.
// The command is a platform-specific binary ("open" or "xdg-open")
// with a fully-constructed file:// URL — no user-supplied shell
// metacharacters are possible because the path comes from
// os.TempDir() + a fixed basename. G204 false positive.
func openBrowser(path string) error {
	url := "file://" + path
	var cmd *exec.Cmd
	// Fire-and-forget: the browser process must outlive this CLI
	// invocation, so a non-cancelable Background ctx is the right
	// semantic. Using CommandContext satisfies the noctx linter.
	ctx := context.Background()
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url) //nolint:gosec // G204: path is os.TempDir() + fixed basename, not user input
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url) //nolint:gosec // G204: same rationale as darwin
	default:
		return fmt.Errorf("unsupported platform for --browser: %s", runtime.GOOS)
	}
	return cmd.Start()
}

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair with WhatsApp by scanning a QR code or entering a phone number",
	Long: `Pair this wad daemon with a WhatsApp account.

Local daemon (default):

  wa pair
  wa pair --browser
  wa pair --phone +5511999999999

Remote daemon over SSH (feature 110e). Drives a daemon deployed on a
Dokku host without leaving your workstation. Value shape:
<ssh-host>:<dokku-app>. SSH host can be a ~/.ssh/config alias, FQDN,
user@host, or Tailscale name:

  wa pair --remote ProxMox.Dokku:wa-burocracy
  wa pair --remote ProxMox.Dokku:wa-burocracy --browser
  wa pair --remote ProxMox.Dokku:wa-burocracy --phone +5511999999999

Pair refuses --remote https://... URL forms — pair requires SSH access
to the daemon's host, not the REST endpoint. Use the <host>:<app>
form for the SSH path; see spec 110c for non-pair --remote URL usage.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Feature 110e: SSH-chain short-circuit. When `--remote` is set,
		// `wa pair` exec's `ssh -t <host> dokku enter <app> -- wa pair`
		// and forwards stdio. Daemon-side untouched (FR-007); the
		// existing socket path below is skipped entirely.
		if pairRemote != "" {
			target, err := ParseRemoteTarget(pairRemote)
			if err != nil {
				rpe := asRemoteParseError(err)
				if rpe != nil {
					return exiterr(rpe.ExitCode(), err)
				}
				return exiterr(64, err)
			}
			extra := buildPairExtraFlags()
			exitCode, runErr := runPairRemote(target, extra)
			if runErr != nil {
				return exiterr(exitCode, runErr)
			}
			return nil
		}

		// Feature 110f: --reset short-circuit. Unlink the daemon's current
		// device server-side (whatsmeow Logout → Store.Delete) so the
		// subsequent `pair` RPC sees Store.ID == nil and generates a
		// fresh QR. messages.db survives because Store.Delete only
		// touches the whatsmeow session SQLite, not the history one.
		if pairReset {
			fmt.Fprintln(os.Stderr, "Resetting device: server-side unlink + session ratchet clear (messages.db preserved)…")
			if _, exitCode, err := callAndClose(flagSocket, "session.logout", map[string]any{}); err != nil {
				return exiterr(exitCode, fmt.Errorf("pair --reset: session.logout: %w", err))
			}
			fmt.Fprintln(os.Stderr, "Device unlinked. Starting fresh pair…")
		}

		if pairBrowser {
			path := pairHTMLPath()
			if err := writeLoadingHTML(path); err != nil {
				fmt.Fprintf(os.Stderr, "warn: could not write loading page: %v\n", err)
			}
			if err := openBrowser(path); err != nil {
				fmt.Fprintf(os.Stderr, "warn: could not open browser: %v\n", err)
				fmt.Fprintf(os.Stderr, "open this manually: file://%s\n", path)
			} else {
				fmt.Fprintf(os.Stderr, "Opened %s in your browser. Waiting for QR code…\n", "file://"+path)
			}
		}

		params := map[string]any{}
		if pairPhone != "" {
			params["phone"] = pairPhone
		}
		if pairIdempotencyKey != "" {
			params["idempotencyKey"] = pairIdempotencyKey
		}

		result, exitCode, err := callAndClose(flagSocket, "pair", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("pair", result, flagJSON))
		return nil
	},
}

func init() {
	pairCmd.Flags().StringVar(&pairPhone, "phone", "", "E.164 phone number for phone-code pairing flow")
	pairCmd.Flags().BoolVar(&pairBrowser, "browser", false, "Open the QR code in your default browser (recommended)")
	pairCmd.Flags().StringVar(&pairIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	pairCmd.Flags().StringVar(&pairRemote, "remote", "",
		"Drive a remote daemon over SSH. Format: <ssh-host>:<dokku-app>. "+
			"Example: --remote ProxMox.Dokku:wa-burocracy. "+
			"Refuses HTTP/HTTPS URL form — pair requires SSH access to the daemon's host, not the REST endpoint.")
	pairCmd.Flags().BoolVar(&pairReset, "reset", false,
		"Unlink the daemon's current device server-side, clear its session ratchet, then pair fresh. "+
			"Preserves messages.db / audit.log / contacts.db (no full wipe — use `wa panic` for that). "+
			"Use this when WhatsApp shows the device as offline / un-linked from your phone but the daemon still reports paired:true. Feature 110f.")
}
