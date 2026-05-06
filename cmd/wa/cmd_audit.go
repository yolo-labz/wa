package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
)

// auditCmd is the parent for `wa audit <subcommand>` operations. The
// only verb at v0 is `verify`; future verbs (rotate from CLI, tail) go
// here too. Spec 016 FR-015 / T054.
var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Inspect the daemon's audit log",
}

var (
	auditVerifyPath    string
	auditVerifyKeyPath string
)

// auditVerifyCmd implements `wa audit verify`. It walks the audit log
// computing the HMAC chain and exits 0 only if every line verifies.
// Operators run this on a rotated file before archiving it.
var auditVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Walk the audit log HMAC chain and report mismatches",
	Long: `Walk the audit log file computing the HMAC chain.

Exits 0 if every line in the file is HMAC-verified, non-zero on the
first detected mismatch (with the offending line number). Empty files
verify as 0 lines.

The HMAC key defaults to the sibling ".key" file the daemon created on
first start; pass --key <path> to override (useful when verifying an
archived audit log against an out-of-band key escrow).
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path := auditVerifyPath
		if path == "" {
			path = defaultAuditPath()
		}
		keyPath := auditVerifyKeyPath
		if keyPath == "" {
			keyPath = path + ".key"
		}
		keyData, err := os.ReadFile(keyPath) //nolint:gosec // operator-supplied
		if err != nil {
			return exiterr(78, fmt.Errorf("read key %s: %w", keyPath, err))
		}
		key, err := decodeHexBytes(strings.TrimSpace(string(keyData)))
		if err != nil {
			return exiterr(78, fmt.Errorf("parse key %s: %w", keyPath, err))
		}
		verified, err := slogaudit.VerifyChain(path, key)
		if err != nil {
			return exiterr(1, fmt.Errorf("verify %s: %w", path, err))
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ok: %d records verified at %s\n", verified, path)
		return nil
	},
}

func init() {
	auditCmd.AddCommand(auditVerifyCmd)
	auditVerifyCmd.Flags().StringVar(&auditVerifyPath, "path", "",
		"path to the audit.log file (default: $XDG_STATE_HOME/wa/<profile>/audit.log)")
	auditVerifyCmd.Flags().StringVar(&auditVerifyKeyPath, "key", "",
		"path to the HMAC key file (default: <path>.key)")
	rootCmd.AddCommand(auditCmd)
}

// defaultAuditPath mirrors the daemon's per-profile audit log location.
// Kept in cmd/wa rather than imported so that `wa audit verify` works
// without depending on internal/observability or wad-specific config.
func defaultAuditPath() string {
	profile := flagProfile
	if profile == "" {
		profile = os.Getenv("WA_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	return filepath.Join(xdg.StateHome, "wa", profile, "audit.log")
}

func decodeHexBytes(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex string %d chars (odd)", len(s))
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		var b byte
		for j := range 2 {
			c := s[i*2+j]
			b <<= 4
			switch {
			case c >= '0' && c <= '9':
				b |= c - '0'
			case c >= 'a' && c <= 'f':
				b |= c - 'a' + 10
			case c >= 'A' && c <= 'F':
				b |= c - 'A' + 10
			default:
				return nil, fmt.Errorf("invalid hex char %q at %d", c, i*2+j)
			}
		}
		out[i] = b
	}
	return out, nil
}
