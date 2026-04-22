package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var debugPprofSeconds int

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Daemon diagnostic helpers",
}

var debugPprofCmd = &cobra.Command{
	Use:       "pprof [cpu|heap|goroutine|block|mutex]",
	Short:     "Capture a runtime profile from wad and open it in `go tool pprof`",
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"cpu", "heap", "goroutine", "block", "mutex"},
	RunE: func(_ *cobra.Command, args []string) error {
		kind := args[0]
		params := map[string]any{"kind": kind}
		if kind == "cpu" && debugPprofSeconds > 0 {
			params["seconds"] = debugPprofSeconds
		}

		raw, exitCode, err := callAndClose(flagSocket, "debug.pprof.profile", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		var res struct {
			Profile string `json:"profile"`
			Bytes   int    `json:"bytes"`
			Kind    string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return exiterr(1, fmt.Errorf("decode response: %w", err))
		}

		blob, err := base64.StdEncoding.DecodeString(res.Profile)
		if err != nil {
			return exiterr(1, fmt.Errorf("decode profile: %w", err))
		}

		tmp, err := os.CreateTemp("", fmt.Sprintf("wa-pprof-%s-*.pb.gz", res.Kind))
		if err != nil {
			return exiterr(1, fmt.Errorf("tempfile: %w", err))
		}
		defer func() { _ = tmp.Close() }()
		if _, err := tmp.Write(blob); err != nil {
			return exiterr(1, fmt.Errorf("write tempfile: %w", err))
		}
		tmpPath, _ := filepath.Abs(tmp.Name())

		if flagJSON {
			fmt.Printf(`{"schema":"wa.debug.pprof/v1","kind":%q,"bytes":%d,"path":%q}`+"\n", res.Kind, res.Bytes, tmpPath)
			return nil
		}

		fmt.Printf("profile %s (%d bytes) written to %s\n", res.Kind, res.Bytes, tmpPath)

		// Shell out to `go tool pprof` if available; otherwise just print
		// the path so the operator can open it manually.
		goBin, err := exec.LookPath("go")
		if err != nil {
			fmt.Println("`go` not found on PATH; open the file manually with `go tool pprof`")
			return nil
		}
		pp := exec.Command(goBin, "tool", "pprof", tmpPath) //nolint:gosec // goBin comes from exec.LookPath, tmpPath from os.CreateTemp
		pp.Stdin = os.Stdin
		pp.Stdout = os.Stdout
		pp.Stderr = os.Stderr
		if err := pp.Run(); err != nil {
			return exiterr(1, fmt.Errorf("go tool pprof: %w", err))
		}
		return nil
	},
}

func init() {
	debugPprofCmd.Flags().IntVar(&debugPprofSeconds, "seconds", 0, "CPU profile window in seconds (cpu only; default 30)")
	debugCmd.AddCommand(debugPprofCmd)
	rootCmd.AddCommand(debugCmd)
}
