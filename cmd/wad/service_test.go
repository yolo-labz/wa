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
	err := runInstallService(true, DefaultProfile) // dry-run to avoid side effects
	if err != nil {
		t.Fatalf("runInstallService(dryRun=true) error as non-root: %v", err)
	}
}

// TestGenerateServiceFile_Hardening asserts the feature-008 hardening
// directives are present in the Linux template (FR-034) and that the
// forbidden `MemoryDenyWriteExecute` is absent.
func TestGenerateServiceFile_Hardening(t *testing.T) {
	content, err := generateServiceFileFor("work")
	if err != nil {
		t.Fatalf("generateServiceFileFor: %v", err)
	}

	if isDarwin() {
		// darwin plist: assert KeepAlive is a dict, not bare bool.
		// The KeepAlive dict contains SuccessfulExit/Crashed keys; bare
		// bool would just be `<true/>` on its own line under the key.
		if !strings.Contains(content, "<key>Crashed</key>") {
			t.Error("plist KeepAlive does not contain Crashed key (FR-035)")
		}
		if !strings.Contains(content, "<key>SuccessfulExit</key>") {
			t.Error("plist KeepAlive does not contain SuccessfulExit key (FR-035)")
		}
		if !strings.Contains(content, "<key>ProcessType</key>") ||
			!strings.Contains(content, "Background") {
			t.Error("plist missing ProcessType=Background (FR-035)")
		}
		if !strings.Contains(content, "EnvironmentVariables") ||
			!strings.Contains(content, "PATH") {
			t.Error("plist missing EnvironmentVariables.PATH (FR-035)")
		}
		// FR-035 forbids SETTING LimitLoadToSessionType — check for the
		// <key> element, not just the string (the template has an
		// explanatory comment that contains the word).
		if strings.Contains(content, "<key>LimitLoadToSessionType</key>") {
			t.Error("plist MUST NOT set LimitLoadToSessionType (FR-035)")
		}
		// Profile name must appear in ProgramArguments.
		if !strings.Contains(content, "<string>--profile</string>") ||
			!strings.Contains(content, "<string>work</string>") {
			t.Error("plist ProgramArguments missing --profile work")
		}
	} else {
		// Linux template unit: assert hardening directives (FR-034).
		must := []string{
			"NoNewPrivileges=yes",
			"LockPersonality=yes",
			"RestrictRealtime=yes",
			"RestrictSUIDSGID=yes",
			"SystemCallFilter=@system-service",
			"SystemCallArchitectures=native",
			"Restart=on-failure",
			"--profile %i",
		}
		for _, directive := range must {
			if !strings.Contains(content, directive) {
				t.Errorf("unit template missing %q (FR-034)", directive)
			}
		}
		// Forbidden: MemoryDenyWriteExecute (Go runtime incompatible).
		if strings.Contains(content, "MemoryDenyWriteExecute") {
			t.Error("unit MUST NOT set MemoryDenyWriteExecute (FR-034, systemd#3814)")
		}
		// Forbidden: mount-namespace directives that no-op in user units.
		for _, forbidden := range []string{"ProtectSystem=strict", "ProtectHome=", "PrivateTmp=", "PrivateDevices=", "RestrictNamespaces="} {
			if strings.Contains(content, forbidden) {
				t.Errorf("unit MUST NOT set %q in user mode (FR-034)", forbidden)
			}
		}
	}
}

// TestGenerateServiceFile_ProfileValidation ensures the install command
// rejects an invalid profile name before rendering the template (FR-049
// argv-only interpolation is enforced by ValidateProfileName upstream).
func TestGenerateServiceFile_ProfileValidation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot test root refusal as root")
	}
	// Invalid profile name: regex rejection happens at runInstallService
	// BEFORE the template renders.
	err := runInstallService(true, "Bad Name")
	if err == nil {
		t.Fatal("runInstallService with invalid profile = nil, want error")
	}
	if !strings.Contains(err.Error(), "profile") {
		t.Errorf("error does not mention profile: %v", err)
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
