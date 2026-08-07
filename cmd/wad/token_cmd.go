// Spec 110d — `wad token issue|revoke|list|sweep` admin subcommands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
)

// handleTokenCommand routes `wad token <verb>` invocations and exits
// the process when matched. Returns false when os.Args[1] != "token"
// so main() falls through to the daemon path. The verbs operate
// directly on the token database file at WAD_REST_TOKEN_DB; the
// daemon does NOT need to be running.
func handleTokenCommand() bool {
	if len(os.Args) < 2 || os.Args[1] != "token" {
		return false
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, tokenUsage())
		os.Exit(64)
	}
	verb := os.Args[2]
	rest := os.Args[3:]

	switch verb {
	case "issue":
		exitOnErr(runTokenIssue(rest))
	case "revoke":
		exitOnErr(runTokenRevoke(rest))
	case "list":
		exitOnErr(runTokenList(rest))
	case "sweep":
		exitOnErr(runTokenSweep(rest))
	case "-h", "--help", "help":
		fmt.Println(tokenUsage())
	default:
		fmt.Fprintf(os.Stderr, "wad token: unknown verb %q\n\n%s\n", verb, tokenUsage())
		os.Exit(64)
	}
	return true
}

func tokenUsage() string {
	return strings.Join([]string{
		"usage: wad token <verb> [flags]",
		"",
		"verbs:",
		"  issue   --name LABEL --scope read|send|admin [--ttl 30d] [--db PATH]",
		"  revoke  --id TOKEN_ID [--db PATH]",
		"  list    [--db PATH] [--json]",
		"  sweep   [--older-than 168h] [--db PATH]",
		"",
		"--db defaults to $WAD_REST_TOKEN_DB.",
	}, "\n")
}

func tokenDBPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if env := os.Getenv("WAD_REST_TOKEN_DB"); env != "" {
		return env, nil
	}
	return "", errors.New("--db not set and WAD_REST_TOKEN_DB env is empty")
}

// openTokenStore resolves the db path and opens the token store with a
// fresh context. The caller owns cancel and Close.
func openTokenStore(dbPath string, timeout time.Duration) (*sqlitetokens.Store, context.Context, func(), error) {
	path, err := tokenDBPath(dbPath)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	store, err := sqlitetokens.Open(ctx, path)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	closer := func() {
		cancel()
		_ = store.Close()
	}
	return store, ctx, closer, nil
}

func runTokenIssue(args []string) error {
	fs := flag.NewFlagSet("wad token issue", flag.ExitOnError)
	var (
		name   = fs.String("name", "", "operator-supplied label (required)")
		scope  = fs.String("scope", "read", "read|send|admin")
		ttl    = fs.String("ttl", "0", "lifetime (e.g. 30d, 24h, 0=never)")
		dbPath = fs.String("db", "", "path to tokens.db (default: $WAD_REST_TOKEN_DB)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("wad token issue: --name is required")
	}
	sc := sqlitetokens.Scope(*scope)
	if !sc.IsValid() {
		return fmt.Errorf("wad token issue: invalid --scope %q (read|send|admin)", *scope)
	}
	dur, err := parseTokenTTL(*ttl)
	if err != nil {
		return err
	}
	store, ctx, closer, err := openTokenStore(*dbPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer closer()

	tok, err := store.Issue(ctx, *name, sc, dur)
	if err != nil {
		return fmt.Errorf("wad token issue: %w", err)
	}

	out := map[string]any{
		"id":        tok.ID,
		"name":      tok.Name,
		"scope":     string(tok.Scope),
		"createdAt": tok.CreatedAt.Format(time.RFC3339),
		"token":     tok.Raw,
	}
	if !tok.ExpiresAt.IsZero() {
		out["expiresAt"] = tok.ExpiresAt.Format(time.RFC3339)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("wad token revoke", flag.ExitOnError)
	var (
		id     = fs.String("id", "", "token id (required)")
		dbPath = fs.String("db", "", "path to tokens.db (default: $WAD_REST_TOKEN_DB)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("wad token revoke: --id is required")
	}
	store, ctx, closer, err := openTokenStore(*dbPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer closer()

	if err := store.Revoke(ctx, *id); err != nil {
		return fmt.Errorf("wad token revoke: %w", err)
	}
	fmt.Printf("revoked %s\n", *id)
	return nil
}

func runTokenList(args []string) error {
	fs := flag.NewFlagSet("wad token list", flag.ExitOnError)
	var (
		dbPath = fs.String("db", "", "path to tokens.db (default: $WAD_REST_TOKEN_DB)")
		asJSON = fs.Bool("json", false, "emit JSON instead of a human table")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, ctx, closer, err := openTokenStore(*dbPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer closer()

	rows, err := store.List(ctx)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSCOPE\tCREATED\tEXPIRES\tLAST USED\tREVOKED")
	for _, t := range rows {
		exp := "-"
		if !t.ExpiresAt.IsZero() {
			exp = t.ExpiresAt.Format("2006-01-02")
		}
		used := "-"
		if !t.LastUsedAt.IsZero() {
			used = t.LastUsedAt.Format("2006-01-02 15:04")
		}
		rev := "-"
		if !t.RevokedAt.IsZero() {
			rev = t.RevokedAt.Format("2006-01-02")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Name, string(t.Scope),
			t.CreatedAt.Format("2006-01-02"), exp, used, rev)
	}
	return w.Flush()
}

func runTokenSweep(args []string) error {
	fs := flag.NewFlagSet("wad token sweep", flag.ExitOnError)
	var (
		dbPath    = fs.String("db", "", "path to tokens.db (default: $WAD_REST_TOKEN_DB)")
		olderThan = fs.String("older-than", "168h", "delete revoked/expired tokens older than this (e.g. 168h, 30d)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	dur, err := parseTokenTTL(*olderThan)
	if err != nil {
		return err
	}

	store, ctx, closer, err := openTokenStore(*dbPath, 10*time.Second)
	if err != nil {
		return err
	}
	defer closer()

	n, err := store.Sweep(ctx, dur)
	if err != nil {
		return err
	}
	fmt.Printf("swept %d row(s)\n", n)
	return nil
}

// parseTokenTTL accepts Go duration syntax (`24h`) plus the `Nd`
// shorthand for days. Returns 0 for "0", "never", or empty input
// (matches store.Issue's never-expires contract).
func parseTokenTTL(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" || raw == "never" {
		return 0, nil
	}
	if before, ok := strings.CutSuffix(raw, "d"); ok {
		days := before
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil || n < 0 {
			return 0, fmt.Errorf("bad TTL %q (e.g. 30d, 24h)", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("bad TTL %q: %w", raw, err)
	}
	if dur < 0 {
		return 0, fmt.Errorf("TTL must be non-negative: %q", raw)
	}
	return dur, nil
}

func exitOnErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
