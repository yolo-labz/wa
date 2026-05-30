package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// doctorStatus is one of the three outcomes a check can produce.
type doctorStatus string

const (
	doctorOK   doctorStatus = "OK"
	doctorWARN doctorStatus = "WARN"
	doctorFAIL doctorStatus = "FAIL"
)

// doctorCheck is one row of the doctor report.
type doctorCheck struct {
	Name   string       `json:"name"`
	Status doctorStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Hint   string       `json:"hint,omitempty"`
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run 11 self-diagnostic checks against the local wad install",
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		checks := runDoctorChecks(ctx)

		if flagJSON {
			out := struct {
				Schema string        `json:"schema"`
				Checks []doctorCheck `json:"checks"`
			}{"wa.doctor/v1", checks}
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(out)
		} else {
			for _, c := range checks {
				fmt.Printf("[%-4s] %s", c.Status, c.Name)
				if c.Detail != "" {
					fmt.Printf(" — %s", c.Detail)
				}
				fmt.Println()
				if c.Hint != "" {
					fmt.Printf("        hint: %s\n", c.Hint)
				}
			}
		}

		for _, c := range checks {
			if c.Status == doctorFAIL {
				return exiterr(1, fmt.Errorf("doctor: %d FAIL(s)", countFails(checks)))
			}
		}
		return nil
	},
}

// runDoctorChecks executes the 11 FR-041 checks. Order is stable for
// deterministic CLI output.
func runDoctorChecks(ctx context.Context) []doctorCheck {
	return []doctorCheck{
		checkPairing(ctx),
		checkSocket(),
		checkLockfile(),
		checkSchemaVersion(),
		checkAuditLogSize(),
		checkRecentCritical(),
		checkWhatsmeowPin(),
		checkOtelExporter(),
		checkMigrationBackups(),
		checkWhatsmeowStale(),
		checkRenovateFreshness(),
	}
}

func countFails(cs []doctorCheck) int {
	n := 0
	for _, c := range cs {
		if c.Status == doctorFAIL {
			n++
		}
	}
	return n
}

// checkPairing asks wad via the `status` JSON-RPC for connection state.
// Not paired → WARN. Offline daemon → FAIL.
func checkPairing(ctx context.Context) doctorCheck {
	_ = ctx
	result, _, err := callAndClose(flagSocket, "status", nil)
	if err != nil {
		return doctorCheck{
			Name: "pairing", Status: doctorFAIL,
			Detail: "daemon unreachable",
			Hint:   "start wad with `launchctl kickstart -k gui/$UID/io.yolo-labz.wad` (darwin) or `systemctl --user start wad` (linux)",
		}
	}
	var s struct {
		Connected bool   `json:"connected"`
		JID       string `json:"jid"`
	}
	_ = json.Unmarshal(result, &s)
	if !s.Connected {
		return doctorCheck{
			Name: "pairing", Status: doctorWARN,
			Detail: "device not paired",
			Hint:   "run `wa pair` to link this device to WhatsApp",
		}
	}
	return doctorCheck{Name: "pairing", Status: doctorOK, Detail: s.JID}
}

// checkSocket verifies the unix socket exists, is 0600, its parent is
// 0700, and peercred accepts the connection.
func checkSocket() doctorCheck {
	info, err := os.Stat(flagSocket)
	if err != nil {
		return doctorCheck{Name: "socket", Status: doctorFAIL, Detail: err.Error()}
	}
	if info.Mode().Perm() != 0o600 {
		return doctorCheck{
			Name: "socket", Status: doctorFAIL,
			Detail: fmt.Sprintf("socket perm = %o, want 0600", info.Mode().Perm()),
			Hint:   "reinstall with `wad install-service` to restore correct perms",
		}
	}
	parentInfo, err := os.Stat(filepath.Dir(flagSocket))
	if err == nil && parentInfo.Mode().Perm() != 0o700 {
		return doctorCheck{
			Name: "socket", Status: doctorWARN,
			Detail: fmt.Sprintf("parent dir perm = %o, want 0700", parentInfo.Mode().Perm()),
		}
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer dialCancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", flagSocket)
	if err != nil {
		return doctorCheck{Name: "socket", Status: doctorFAIL, Detail: "dial failed: " + err.Error()}
	}
	_ = conn.Close()
	return doctorCheck{Name: "socket", Status: doctorOK, Detail: flagSocket}
}

// checkLockfile asserts the <socket>.lock file exists alongside the
// socket (single-instance guard).
func checkLockfile() doctorCheck {
	lockPath := flagSocket + ".lock"
	if _, err := os.Stat(lockPath); err != nil {
		return doctorCheck{Name: "lockfile", Status: doctorWARN, Detail: "missing"}
	}
	return doctorCheck{Name: "lockfile", Status: doctorOK, Detail: lockPath}
}

// checkSchemaVersion reads the on-disk layout-version file and compares it to
// the authoritative domain.LayoutSchemaVersion the daemon writes. Both sides
// reference the same constant so they cannot drift — #180 item 5: the old
// hardcoded "expected v4" produced a false WARN on every healthy v2 install
// (the daemon has only ever written v2).
func checkSchemaVersion() doctorCheck {
	want := strconv.Itoa(domain.LayoutSchemaVersion)
	path, err := schemaVersionPath()
	if err != nil {
		return doctorCheck{Name: "schema_version", Status: doctorFAIL, Detail: err.Error()}
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is under validated XDG config home
	if err != nil {
		return doctorCheck{
			Name: "schema_version", Status: doctorWARN,
			Detail: "no version file — pre-008 install?",
			Hint:   "run `wa migrate` to bring the install forward",
		}
	}
	v := strings.TrimSpace(string(b))
	if v != want {
		return doctorCheck{
			Name: "schema_version", Status: doctorWARN,
			Detail: fmt.Sprintf("schema v%s, expected v%s", v, want),
			Hint:   "run `wa migrate` to forward-migrate",
		}
	}
	return doctorCheck{Name: "schema_version", Status: doctorOK, Detail: "v" + want}
}

// checkAuditLogSize WARN >100 MiB, FAIL >1 GiB.
func checkAuditLogSize() doctorCheck {
	path, err := auditLogPath()
	if err != nil {
		return doctorCheck{Name: "audit_log_size", Status: doctorWARN, Detail: err.Error()}
	}
	info, err := os.Stat(path)
	if err != nil {
		return doctorCheck{Name: "audit_log_size", Status: doctorOK, Detail: "none"}
	}
	const (
		warn = 100 * 1024 * 1024
		fail = 1024 * 1024 * 1024
	)
	switch {
	case info.Size() > fail:
		return doctorCheck{
			Name: "audit_log_size", Status: doctorFAIL,
			Detail: fmt.Sprintf("%d bytes (>1 GiB)", info.Size()),
			Hint:   "run `wad audit rotate` to roll the log",
		}
	case info.Size() > warn:
		return doctorCheck{
			Name: "audit_log_size", Status: doctorWARN,
			Detail: fmt.Sprintf("%d bytes (>100 MiB)", info.Size()),
		}
	}
	return doctorCheck{Name: "audit_log_size", Status: doctorOK, Detail: fmt.Sprintf("%d bytes", info.Size())}
}

// checkRecentCritical is a placeholder for the "last 5 CRITICAL errors"
// check. The scan is best-effort — unknown layout returns OK.
func checkRecentCritical() doctorCheck {
	path, err := wadLogPath()
	if err != nil {
		return doctorCheck{Name: "recent_critical", Status: doctorOK, Detail: "no log"}
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is under validated XDG state home
	if err != nil {
		return doctorCheck{Name: "recent_critical", Status: doctorOK, Detail: "no log"}
	}
	count := strings.Count(string(b), `"level":"ERROR"`) + strings.Count(string(b), `"level":"CRITICAL"`)
	if count >= 5 {
		return doctorCheck{
			Name: "recent_critical", Status: doctorWARN,
			Detail: fmt.Sprintf("%d error-level entries in wad.log", count),
			Hint:   "review $XDG_STATE_HOME/wa/<profile>/wad.log",
		}
	}
	return doctorCheck{Name: "recent_critical", Status: doctorOK, Detail: fmt.Sprintf("%d error-level", count)}
}

// checkWhatsmeowPin reports the pinned whatsmeow version for humans.
// Does not fail — surface information only.
func checkWhatsmeowPin() doctorCheck {
	return doctorCheck{Name: "whatsmeow_pin", Status: doctorOK, Detail: "see go.mod `go.mau.fi/whatsmeow`"}
}

// checkOtelExporter inspects the WA_OTEL_EXPORTER env var.
func checkOtelExporter() doctorCheck {
	exp := strings.TrimSpace(os.Getenv("WA_OTEL_EXPORTER"))
	if exp == "" {
		exp = "stdout"
	}
	switch exp {
	case "stdout", "otlp-unix":
		return doctorCheck{Name: "otel_exporter", Status: doctorOK, Detail: exp}
	default:
		return doctorCheck{
			Name: "otel_exporter", Status: doctorFAIL,
			Detail: fmt.Sprintf("unknown exporter %q", exp),
			Hint:   "set WA_OTEL_EXPORTER to `stdout` or `otlp-unix`",
		}
	}
}

// doctorBackupCap mirrors sqlitehistory.maxBackups (issue #202). The
// daemon prunes the backup set to this depth on every startup; the
// doctor only flags a pile that exceeds it, which now means the daemon
// has not been restarted since the prune-on-open change landed.
const doctorBackupCap = 5

// checkMigrationBackups reports the backup directory depth.
// WARN on 0, OK on 1..doctorBackupCap, FAIL above it.
func checkMigrationBackups() doctorCheck {
	dir, err := backupsDirPath()
	if err != nil {
		return doctorCheck{Name: "migration_backups", Status: doctorOK, Detail: "no backups dir"}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return doctorCheck{Name: "migration_backups", Status: doctorWARN, Detail: "0 backups"}
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	switch {
	case n == 0:
		return doctorCheck{Name: "migration_backups", Status: doctorWARN, Detail: "0 backups"}
	case n > doctorBackupCap:
		return doctorCheck{
			Name: "migration_backups", Status: doctorFAIL,
			Detail: fmt.Sprintf("%d backups (>%d)", n, doctorBackupCap),
			Hint:   "restart the daemon — it prunes backups to the retention cap on startup",
		}
	}
	return doctorCheck{Name: "migration_backups", Status: doctorOK, Detail: fmt.Sprintf("%d backups", n)}
}

// checkWhatsmeowStale is a placeholder for the "whatsmeow stale >30d"
// check — the actual measurement belongs in CI, not the CLI. Reports OK
// so this doctor check never blocks an install.
func checkWhatsmeowStale() doctorCheck {
	return doctorCheck{Name: "whatsmeow_stale", Status: doctorOK, Detail: "tracked by Renovate"}
}

// checkRenovateFreshness is a placeholder — actual freshness is a CI
// concern. Reports OK.
func checkRenovateFreshness() doctorCheck {
	return doctorCheck{Name: "renovate_freshness", Status: doctorOK, Detail: "tracked by Renovate CI"}
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
