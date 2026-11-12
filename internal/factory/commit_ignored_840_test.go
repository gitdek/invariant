package factory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A file a build wrote is in its commit whatever the repository ignores
// (#840). Without that, a plan's coverage.go under a coverage.* rule was
// left out of the published branch, which then didn't compile. What the
// build didn't write stays ignored, and so does everything for a commit
// that names no files.
func TestCommitHoldsAnIgnoredFileTheBuildWrote840(t *testing.T) {
	ctx := context.Background()
	_, clone := gitRepos(t)
	wt, err := clone.Worktree(ctx, "invariant/issue-840-coverage", "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	defer clone.RemoveWorktree(ctx, wt)
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(wt, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree := func() []string {
		t.Helper()
		return strings.Fields(git(t, wt, "ls-tree", "-r", "--name-only", "HEAD"))
	}
	write(".gitignore", "coverage.*\nbin/\n")
	if _, err := clone.Commit(ctx, wt, ".", "Ignore coverage profiles"); err != nil {
		t.Fatal(err)
	}
	write("verify/coverage.go", "package verify\n")
	write("verify/verify.go", "package verify\n")
	write("verify/coverage.out", "mode: set\n")
	write("bin/tool", "binary\n")
	// The build wrote two files and removed a third it had made earlier.
	if _, err := clone.Commit(ctx, wt, ".", "Implement the plan", "verify/coverage.go", "verify/verify.go", "verify/removed.go"); err != nil {
		t.Fatal(err)
	}
	got := tree()
	for _, want := range []string{"verify/coverage.go", "verify/verify.go"} {
		if !slices.Contains(got, want) {
			t.Errorf("the commit lacks %s, which the build wrote: %v", want, got)
		}
	}
	for _, not := range []string{"verify/coverage.out", "bin/tool"} {
		if slices.Contains(got, not) {
			t.Errorf("the commit holds %s, which the build didn't write and the repository ignores", not)
		}
	}
	// A commit that names no files leaves what's ignored out, as before.
	write("docs/coverage.md", "# notes\n")
	write("docs/notes.md", "# notes\n")
	if _, err := clone.Commit(ctx, wt, ".", "Add notes"); err != nil {
		t.Fatal(err)
	}
	if got := tree(); slices.Contains(got, "docs/coverage.md") || !slices.Contains(got, "docs/notes.md") {
		t.Errorf("a commit naming no files changed what it holds: %v", got)
	}
}
