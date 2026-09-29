package factory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// A watcher that stopped mid-build leaves its worktrees registered in its
// clone: one whose directory is still there, and one whose directory is gone.
// Clearing them frees their branches for a new worktree, and leaves the clone
// with no worktree but its own and the new one.
func TestClearWorktreesFreesALeftoverBranch(t *testing.T) {
	ctx := context.Background()
	_, clone := gitRepos(t)
	kept, err := clone.Worktree(ctx, "invariant/issue-1-buffer", "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := clone.Worktree(ctx, "invariant/issue-2-queue", "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	if err := clone.ClearWorktrees(ctx); err != nil {
		t.Fatalf("ClearWorktrees = %v", err)
	}
	if _, err := os.Stat(kept); !os.IsNotExist(err) {
		t.Errorf("the leftover worktree %s is still on disk: %v", kept, err)
	}
	if list := git(t, clone.Dir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("after clearing, the clone still holds other worktrees:\n%s", list)
	}
	for _, branch := range []string{"invariant/issue-1-buffer", "invariant/issue-2-queue"} {
		wt, err := clone.Worktree(ctx, branch, "origin/main")
		if err != nil {
			t.Fatalf("a worktree for %s after clearing: %v", branch, err)
		}
		defer clone.RemoveWorktree(ctx, wt)
	}
	// Clearing a clone with no leftovers does nothing, and doesn't fail.
	if err := clone.ClearWorktrees(ctx); err != nil {
		t.Fatalf("ClearWorktrees = %v", err)
	}
}

// prWatch ends a watch once the factory posts its pull request.
type prWatch struct {
	GitHub
	stop func()
}

func (g prWatch) PostComment(ctx context.Context, issue int, body string) (github.Comment, error) {
	c, err := g.GitHub.PostComment(ctx, issue, body)
	if m, ok := DecodeMarker(body); ok && err == nil && m.Kind == KindPR {
		g.stop()
	}
	return c, err
}

// With a leftover worktree holding an issue's branch, as a watcher that
// stopped mid-build leaves it, the watcher adds a worktree for that branch
// and builds.
func TestWatchBuildsPastALeftoverWorktree(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.gh.open(1, "gitdek", "Add a bounded buffer", "Producers put log lines in a buffer; a shipper takes them out in order.\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	p := proposal.Marker.Proposal

	leftover, err := r.f.Repo.(Clone).Worktree(context.Background(), "invariant/issue-1-"+p.Slug, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("a stopped watcher's worktree: %s", leftover)

	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r.f.GitHub = prWatch{r.gh, cancel}
	r.f.Watch(ctx, 20*time.Millisecond)

	if len(r.build.built) != 1 {
		t.Fatalf("builds = %v; want one, past the leftover worktree", r.build.built)
	}
	r.expect(1, KindPR, LabelPR)
}
