package main

import (
	"strings"
	"testing"
)

// TestParseRemoteTarget walks every row of data-model.md §Test
// coverage matrix. Feature 110e FR-002 + FR-003.
func TestParseRemoteTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantHost      string
		wantApp       string
		wantErr       bool
		wantExitCode  int
		wantErrSubstr string
	}{
		{
			name:     "HappyPath",
			input:    "ProxMox.Dokku:wa-burocracy",
			wantHost: "ProxMox.Dokku",
			wantApp:  "wa-burocracy",
		},
		{
			name:     "UserAtHost",
			input:    "pedro@host.example:wa-personal",
			wantHost: "pedro@host.example",
			wantApp:  "wa-personal",
		},
		{
			name:     "MultiColonApp",
			input:    "host:app:extra",
			wantHost: "host",
			wantApp:  "app:extra",
		},
		{
			name:          "EmptyRejected",
			input:         "",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "empty string",
		},
		{
			name:          "MissingSeparator",
			input:         "no-colon",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "missing ':' separator",
		},
		{
			name:          "EmptyHost",
			input:         ":app",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "empty host",
		},
		{
			name:          "EmptyApp",
			input:         "host:",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "empty app",
		},
		{
			name:          "URLRejectedHTTPS",
			input:         "https://wa.example.com",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "pair requires SSH access",
		},
		{
			name:          "URLRejectedHTTP",
			input:         "http://wa.example.com",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "pair requires SSH access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRemoteTarget(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tt.input)
				}
				rpe := asRemoteParseError(err)
				if rpe == nil {
					t.Fatalf("expected *remoteParseError, got %T: %v", err, err)
				}
				if rpe.ExitCode() != tt.wantExitCode {
					t.Errorf("ExitCode = %d, want %d", rpe.ExitCode(), tt.wantExitCode)
				}
				if !strings.Contains(rpe.Error(), tt.wantErrSubstr) {
					t.Errorf("Error %q does not contain %q", rpe.Error(), tt.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tt.input, err)
			}
			if got.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, tt.wantHost)
			}
			if got.App != tt.wantApp {
				t.Errorf("App = %q, want %q", got.App, tt.wantApp)
			}
		})
	}
}

// TestPairHelpShowsRemoteFlag asserts the `--remote` flag is
// discoverable in `wa pair --help`. FR-001 + SC-003.
func TestPairHelpShowsRemoteFlag(t *testing.T) {
	t.Parallel()
	help := pairCmd.UsageString()
	for _, want := range []string{
		"--remote",
		"ProxMox.Dokku:wa-burocracy",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("pair --help missing %q.\nUsage was:\n%s", want, help)
		}
	}
}
