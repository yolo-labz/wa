package main

import (
	"context"
	"encoding/json"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTokenDraftCLIHelper(t *testing.T) {
	if os.Getenv("WA_DRAFT_CLI_HELPER") != "1" {
		return
	}
	err := runTokenIssue([]string{
		"--name", "synthetic cli draft", "--scope", os.Getenv("WA_DRAFT_CLI_SCOPE"),
		"--db", os.Getenv("WA_DRAFT_CLI_DB"),
	})
	if err != nil {
		os.Exit(22)
	}
	os.Exit(0)
}

func TestTokenDraftCLIValidation(t *testing.T) {
	db := filepath.Join(t.TempDir(), "synthetic-token-store.db")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(scope string) ([]byte, error) {
		cmd := exec.Command(self, "-test.run=^TestTokenDraftCLIHelper$")
		cmd.Env = append(os.Environ(),
			"WA_DRAFT_CLI_HELPER=1", "WA_DRAFT_CLI_SCOPE="+scope, "WA_DRAFT_CLI_DB="+db)
		return cmd.Output()
	}
	if _, err := invoke("unsupported"); err == nil {
		t.Fatal("invalid CLI scope issued")
	}
	output, err := invoke("draft")
	if err != nil {
		t.Fatalf("draft CLI scope rejected: %v", err)
	}
	var view struct {
		Scope string `json:"scope"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(output, &view); err != nil {
		t.Fatal("synthetic CLI response invalid JSON")
	}
	if view.Scope != "draft" || view.Token == "" {
		t.Fatal("synthetic draft token missing")
	}
	store, err := sqlitetokens.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tok, err := store.Verify(context.Background(), view.Token)
	if err != nil || tok.Scope != sqlitetokens.ScopeDraft {
		t.Fatalf("CLI token verification failed: %v scope=%s", err, tok.Scope)
	}
	rows, err := store.List(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("invalid scope minted row: %d %v", len(rows), err)
	}
}
