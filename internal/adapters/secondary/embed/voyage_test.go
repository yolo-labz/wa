package embed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestVoyageOptInGated — without voyage.env the loader returns
// ErrCloudOptOut, and an Embedder without APIKey refuses to call.
// Matches tasks-tier3.md T3-04 test name.
func TestVoyageOptInGated(t *testing.T) {
	t.Parallel()

	// --- no voyage.env → ErrCloudOptOut --------------------------------
	dir := t.TempDir()
	_, err := LoadVoyageAPIKey(dir)
	if !errors.Is(err, ErrCloudOptOut) {
		t.Fatalf("no voyage.env: got %v want ErrCloudOptOut", err)
	}

	// --- empty APIKey → Embed refuses without ever hitting network ----
	v := NewVoyage()
	_, err = v.Embed(context.Background(), "hi")
	if !errors.Is(err, ErrCloudOptOut) {
		t.Fatalf("empty APIKey: got %v want ErrCloudOptOut", err)
	}

	// --- voyage.env present → key is returned -------------------------
	path := filepath.Join(dir, "voyage.env")
	if err := os.WriteFile(path, []byte("# comment\nVOYAGE_API_KEY=pa-live-xyz\n"), 0o600); err != nil {
		t.Fatalf("write voyage.env: %v", err)
	}
	key, err := LoadVoyageAPIKey(dir)
	if err != nil {
		t.Fatalf("LoadVoyageAPIKey: %v", err)
	}
	if key != "pa-live-xyz" {
		t.Fatalf("key: got %q want pa-live-xyz", key)
	}
}
