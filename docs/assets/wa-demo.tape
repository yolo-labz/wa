# wa — README demo, recorded with VHS (https://github.com/charmbracelet/vhs)
#
# Regenerate:   ./scripts/record-demo.sh
#
# This tape is the source of truth, not the GIF. It is plain text, diffs in a
# PR, and drives the REAL binaries — which is precisely what the hand-authored
# asciinema cast it replaced failed at: that cast ran `wa daemon status`, a
# command this CLI has never had, and listed 12 commands when there were 47.
#
# Everything here runs against an isolated XDG root under a temp dir, so the
# recording can never touch a real profile, socket, or paired session. The
# daemon it talks to is genuinely unpaired: that is why `doctor` shows one
# WARN. Other diagnostic rows are also captured as-is; no frame is staged.

Output docs/assets/wa-demo.gif
Output docs/assets/wa-demo.mp4
Output docs/assets/wa-demo.webm

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 30ms
Set Framerate 15
Set PlaybackSpeed 1

# The helper passes isolated HOME/XDG + PATH to freshly built binaries.
# This file only sets the prompt; hide scaffolding, never command results.
Hide
Type 'source "$WA_DEMO_ENV"' Enter
Type "clear" Enter
Sleep 500ms
Show

Type "wa --version" Sleep 400ms Enter
Sleep 1200ms

Type "wa doctor" Sleep 400ms Enter
Sleep 4s

Type "wa status" Sleep 400ms Enter
Sleep 1400ms

Type "wa allow list" Sleep 400ms Enter
Sleep 1600ms

# Help only: no message is revoked. Clear so the warning and flags fit.
Type "clear" Enter
Type "wa msg revoke --help" Sleep 400ms Enter
Sleep 5s
