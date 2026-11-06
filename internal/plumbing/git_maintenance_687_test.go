package plumbing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A commit in the sandbox starts no background maintenance, even run as
// the repository's tests run git, with blank global and system config. Git
// otherwise starts its maintenance detached after a commit, and it writes
// into the repository while the test's temporary directory is being
// removed (#687).
func TestSandboxCommitsStartNoMaintenance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	commit := func(env []string) string {
		t.Helper()
		dir := t.TempDir()
		run := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TRACE=1"), env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
			}
			return string(out)
		}
		run("init", "-q")
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
		return run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "m")
	}
	starts := func(trace string) bool {
		return strings.Contains(trace, "maintenance run") || strings.Contains(trace, "gc --auto")
	}
	if !starts(commit(nil)) {
		t.Skip("this git starts no maintenance after a commit, so there's nothing to keep out")
	}
	if trace := commit(sandboxGit); starts(trace) {
		t.Fatalf("a commit in the sandbox's environment starts git's maintenance:\n%s", trace)
	}
}
