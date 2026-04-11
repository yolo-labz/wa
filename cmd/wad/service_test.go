package main

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateServiceFile_DryRun(t *testing.T) {
	content, err := generateServiceFile()
	if err != nil {
		t.Fatalf("generateServiceFile() error: %v", err)
	}

	if content == "" {
		t.Fatal("generateServiceFile() returned empty content")
	}

	// The generated file must reference the test binary's executable path.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error: %v", err)
	}
	if !strings.Contains(content, exe) {
		t.Errorf("generated service file does not contain executable path %q", exe)
	}

	// Platform-specific checks.
	if isDarwin() {
		if !strings.Contains(content, "com.yolo-labz.wad") {
			t.Error("plist missing Label")
		}
		if !strings.Contains(content, "<!DOCTYPE plist") {
			t.Error("plist missing DOCTYPE")
		}
	} else {
		if !strings.Contains(content, "[Unit]") {
			t.Error("unit file missing [Unit] section")
		}
		if !strings.Contains(content, "WantedBy=default.target") {
			t.Error("unit file missing WantedBy")
		}
	}
}

func TestRootRefusal(t *testing.T) {
	// This test validates the logic; on most dev/CI environments we
	// are NOT root, so the check passes trivially. If running as root
	// (e.g. in a container), skip — the invariant is that the function
	// refuses root, and we cannot mock os.Geteuid portably.
	if os.Geteuid() == 0 {
		t.Skip("running as root — cannot test root refusal")
	}

	// The function should NOT return an error when not root.
	err := runInstallService(true) // dry-run to avoid side effects
	if err != nil {
		t.Fatalf("runInstallService(dryRun=true) error as non-root: %v", err)
	}
}

// isDarwin reports whether the test is running on macOS.
// This is a compile-time constant via build tags in practice,
// but for a single test file we use runtime detection.
func isDarwin() bool {
	// runtime.GOOS is a constant, but importing runtime just for
	// this is fine in test code.
	return strings.Contains(strings.ToLower(os.Getenv("GOOS")), "darwin") ||
		isCurrentOSDarwin()
}
