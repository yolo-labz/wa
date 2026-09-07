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
# WARN, and showing it is the point — no frame is staged.

Output docs/assets/wa-demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 30ms
Set PlaybackSpeed 1

# Isolated root + PATH to the freshly built binaries. Hidden so the demo
# opens on a command rather than on scaffolding.
Hide
Type "source /tmp/wa-demo-env/env.sh" Enter
Type "clear" Enter
Sleep 500ms
Show

Type "wa --version" Sleep 400ms Enter
Sleep 1200ms

Type "wa doctor" Sleep 400ms Enter
Sleep 2600ms

Type "wa status" Sleep 400ms Enter
Sleep 1400ms

Type "wa allow list" Sleep 400ms Enter
Sleep 1600ms

Type "wa msg revoke --help" Sleep 400ms Enter
Sleep 3000ms
