package buildstamp_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guardScript is the build-time guard the Dockerfile runs after it derives
// VERSION/COMMIT from the in-context .git. It is resolved to an absolute path
// because the fixtures below run the script inside a temp directory.
func guardScript(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../scripts/verify-git-stamp.sh")
	if err != nil {
		t.Fatalf("resolve guard script: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("guard script missing: %v", err)
	}
	return abs
}

// gitFixture builds a throwaway repository and returns its directory plus a
// helper that runs git there with the caller's identity supplied inline (no
// dependency on the host's ~/.gitconfig).
type gitFixture struct {
	dir string
	t   *testing.T
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	f := &gitFixture{dir: dir, t: t}
	// -c user.* keeps this independent of the host configuration and
	// disables hooks, which in this fleet are repo-wide gates.
	f.git("init", "-q", ".")
	f.git("config", "user.email", "buildstamp-test@example.invalid")
	f.git("config", "user.name", "buildstamp test")
	return f
}

func (f *gitFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = f.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *gitFixture) write(path, content string) {
	f.t.Helper()
	full := filepath.Join(f.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
}

// runScript executes the guard inside the fixture and returns exit code, stdout, stderr.
func (f *gitFixture) runScript(commit string) (int, string, string) {
	f.t.Helper()
	cmd := exec.CommandContext(context.Background(), "sh", guardScript(f.t), commit)
	cmd.Dir = f.dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		f.t.Fatalf("run guard: %v", err)
	}
	return code, stdout.String(), stderr.String()
}

// TestVerifyGitStampRejectsStaleGit is the regression gate for #365: with a
// .git that does not describe the sources in the build context, the guard
// must fail the build rather than let the Dockerfile stamp a wrong commit.
// This is exactly the situation the incident shipped — the image ran code
// from a newer commit while reporting 1a59519 for weeks.
func TestVerifyGitStampRejectsStaleGit(t *testing.T) {
	f := newGitFixture(t)
	f.write("a.txt", "one\n")
	f.write("docs/note.md", "excluded from the context\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	stale := f.git("rev-parse", "HEAD")

	// Sources move on without .git moving with them: a cached checkout, a
	// foreign worktree, or a deploy-hook copy. Exactly the #365 shape.
	f.write("a.txt", "two\n")

	code, _, stderr := f.runScript(stale)
	if code != 1 {
		t.Fatalf("guard exit = %d, want 1 (stale .git must fail the build); stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "a.txt") {
		t.Fatalf("guard stderr does not name the mismatching file: %q", stderr)
	}
	if !strings.Contains(stderr, "--build-arg COMMIT") {
		t.Fatalf("guard stderr does not say how to fix it: %q", stderr)
	}
}

// TestVerifyGitStampAcceptsCleanTree keeps the guard from breaking the normal
// build: a context whose .git matches its sources passes.
func TestVerifyGitStampAcceptsCleanTree(t *testing.T) {
	f := newGitFixture(t)
	f.write("a.txt", "one\n")
	f.write("internal/x/y.go", "package x\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	head := f.git("rev-parse", "HEAD")

	if code, _, stderr := f.runScript(head); code != 0 {
		t.Fatalf("guard exit = %d, want 0 on a clean tree; stderr=%q", code, stderr)
	}
}

// TestVerifyGitStampToleratesDockerignore keeps the guard usable inside the
// image build: .dockerignore removes tracked paths (docs/, specs/, .github/)
// from the context, and their absence must not read as staleness — otherwise
// every Docker build would fail.
func TestVerifyGitStampToleratesDockerignore(t *testing.T) {
	f := newGitFixture(t)
	f.write("a.txt", "one\n")
	f.write("docs/note.md", "excluded from the context\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	head := f.git("rev-parse", "HEAD")

	if err := os.RemoveAll(filepath.Join(f.dir, "docs")); err != nil {
		t.Fatalf("remove docs/: %v", err)
	}

	if code, _, stderr := f.runScript(head); code != 0 {
		t.Fatalf("guard exit = %d, want 0 when only .dockerignore paths are absent; stderr=%q", code, stderr)
	}
}

// TestVerifyGitStampRequiresCommit makes the "no stamp to verify" case loud:
// an unknown commit cannot be checked, so it must not silently pass.
func TestVerifyGitStampRequiresCommit(t *testing.T) {
	f := newGitFixture(t)
	f.write("a.txt", "one\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")

	for _, bad := range []string{"", "unknown", "dev"} {
		if code, _, _ := f.runScript(bad); code != 2 {
			t.Fatalf("guard exit = %d for commit %q, want 2", code, bad)
		}
	}
}
