package factory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// These hold what #790's two reviews found outside the files its plan
// names, which #799 couldn't change (#895).

// review895Link commits a link at path on the rig's base branch.
func review895Link(t *testing.T, r *rig, target, path string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(work, path)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(work, path)); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "a link")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
}

// The rules for a project's directory judge the text of its path. With a
// link a person committed on the base branch, a proposal for a directory
// under it passed them, and the second review's probe had ratification
// write the whole project where the link pointed, outside the worktree. A
// link inside a project that leads out is the same hazard for the build
// that follows. Neither is written through, and the note says what a
// person does, since drafting again wouldn't remove a link.
func TestRatification895FollowsNoLinkOutOfItsWorktree(t *testing.T) {
	t.Run("a link on the way to a new project", func(t *testing.T) {
		_, outside, victim := review799Outside(t)
		result := saved790Result(t, false)
		r := newRig(t)
		saved790Open(t, r)
		review895Link(t, r, outside, "examples/link")
		result.Proposal.Dir = "examples/link/proj"
		if err := destination(result.Proposal); err != nil {
			t.Fatalf("the fixture is refused by its path's text: %v", err)
		}
		r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode())
		review799Refused(t, r, result.Proposal.Hash, "a link on the way to `examples/link/proj` leads out of the repository")
		if note := r.gh.last(790).Comment.Body; !strings.Contains(note, "Once the link is gone") || strings.Contains(note, "/invariant revise") {
			t.Fatalf("the note offers a new draft, which wouldn't remove the link:\n%s", note)
		}
		review799Untouched(t, outside, victim)
	})
	t.Run("a link inside an amended project", func(t *testing.T) {
		_, outside, victim := review799Outside(t)
		r := newRig(t)
		saved790Open(t, r)
		p := review799Amendment(t, r, "examples/saved-reader").Proposal
		review895Link(t, r, outside, "examples/saved-reader/notes")
		r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: p, ReplyTo: []int64{0}}).encode())
		saved790Fresh(t, r)
		review799Refused(t, r, p.Hash, "`examples/saved-reader/notes` is a link that leads out of the repository")
		review799Untouched(t, outside, victim)
	})
	t.Run("a link that stays inside the repository", func(t *testing.T) {
		r := newRig(t)
		saved790Open(t, r)
		p := review799Amendment(t, r, "examples/saved-reader").Proposal
		review895Link(t, r, ".invariant/invariant.json", "examples/saved-reader/manifest")
		r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: p, ReplyTo: []int64{0}}).encode())
		saved790Fresh(t, r)
		r.gh.say(790, "gitdek", "/invariant ratify "+p.Hash)
		r.poll()
		if len(r.build.built) != 1 {
			t.Fatalf("an amendment of a project with a link inside the repository didn't ratify and build:\n%s", r.gh.last(790).Comment.Body)
		}
	})
}

// A build checks out the branch its ratified post names and works under
// the project it names, removing and copying files there. A proposal's own
// directory and slug are checked before ratification, and these, which
// ratification wrote into the post, were used as they were read back. A
// post that names another issue's branch, the base branch, another issue's
// plan or a directory outside the worktree starts no build.
func TestBuild895HoldsARatifiedPostsOwnBranchAndProject(t *testing.T) {
	const good = "invariant/issue-790-reader-lifecycle"
	for _, c := range []struct{ name, branch, project, says string }{
		{"a directory outside the worktree", good, "../%s", "its project directory"},
		{"the repository's own records", good, ".invariant/specs", "its project directory"},
		{"another issue's plan", good, plumbing.LockPath(791), "its plan"},
		{"a plan's path that climbs out", good, plumbing.LockDir + "/../../../%s/issue-790.json", "its plan"},
		{"the base branch", "main", "examples/proj", "its branch"},
		{"another issue's branch", "invariant/issue-791-reader-lifecycle", "examples/proj", "its branch"},
		{"a branch git can't name", "invariant/issue-790-a..b", "examples/proj", "its branch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name, outside, victim := review799Outside(t)
			result := saved790Result(t, false)
			r := newRig(t)
			saved790Open(t, r)
			r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode())
			ratify := r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
			project := c.project
			if strings.Contains(project, "%s") {
				project = strings.Replace(project, "%s", name, 1)
			}
			r.gh.say(790, r.gh.me, (Marker{Kind: KindRatified, ReplyTo: []int64{ratify.ID}, Proposal: result.Proposal,
				Project: project, Branch: c.branch, Hash: result.Proposal.Hash}).encode())
			before := len(r.gh.comments[790])
			r.poll()
			failed := r.gh.last(790)
			if failed.Marker.Kind != KindFailed || failed.Marker.Why() != FailStopped || !strings.Contains(failed.Comment.Body, "I haven't started this build") || !strings.Contains(failed.Comment.Body, c.says) {
				t.Fatalf("the build wasn't refused for %s:\n%s", c.says, failed.Comment.Body)
			}
			r.poll()
			if len(r.gh.comments[790]) != before+1 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatalf("a ratified post that names %s was built, opened a pull request or was answered %d times", c.name, len(r.gh.comments[790])-before)
			}
			review799Untouched(t, outside, victim)
		})
	}
	// What ratification itself writes is taken, for a project and for a plan.
	for _, m := range []Marker{
		{Branch: good, Project: "examples/reader-lifecycle"},
		{Branch: good + "-2", Project: "invariant/05-statue"},
		{Branch: "invariant/issue-790-a-hello-page", Project: plumbing.LockPath(790)},
	} {
		if err := buildDestination(790, m); err != nil {
			t.Errorf("a ratified post for %s on %s is refused: %v", m.Project, m.Branch, err)
		}
	}
}

// review895FlakyGitHub fails a read of an issue's comments when told to, as
// a moment's network trouble does.
type review895FlakyGitHub struct {
	GitHub
	fail func() bool
}

func (g *review895FlakyGitHub) Comments(ctx context.Context, issue int) ([]github.Comment, error) {
	if g.fail != nil && g.fail() {
		return nil, errors.New("GitHub is unreachable for a moment")
	}
	return g.GitHub.Comments(ctx, issue)
}

// A plan's build that has finished isn't lost to one failed read of its
// issue's posts, as a modeled build isn't since #799. Between the agent's
// run and the store of its result, the build asks whether a later post
// superseded it. One read failing there left the run with no result, to be
// reported as stopped partway and built again.
func TestPlanBuild895SurvivesALostLookup(t *testing.T) {
	r, b := plumbingRig(t, "page/page.go")
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal).Marker.Proposal
	failures := 0
	flaky := &review895FlakyGitHub{GitHub: r.f.GitHub}
	flaky.fail = func() bool {
		// The first read after the agent has built fails.
		if len(b.built) == 1 && failures == 0 {
			failures++
			return true
		}
		return false
	}
	r.f.GitHub = flaky
	r.gh.say(1, "gitdek", "/invariant ratify "+p.Hash)
	if err := r.f.Poll(t.Context()); err != nil {
		t.Logf("the poll that built: %v", err)
	}
	flaky.fail = nil
	r.poll()
	last := r.gh.last(1)
	if !reflect.DeepEqual(b.built, []int{1}) || failures != 1 {
		t.Fatalf("the agent built %v, and %d reads were lost", b.built, failures)
	}
	if last.Marker.Kind == KindFailed {
		t.Fatalf("a finished build was reported as failed, %s, after %d lost reads: %s", last.Marker.Why(), failures, last.Comment.Body)
	}
	if len(r.gh.prs) != 1 {
		t.Fatalf("the finished build opened %d pull requests, after %d lost reads", len(r.gh.prs), failures)
	}
}
