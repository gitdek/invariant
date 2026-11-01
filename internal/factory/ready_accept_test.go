package factory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/github"
)

// #369's review: only a person merges a change to the trusted base, a
// modeled project's included, such as one of the factory's own proved cores
// under factory/ (D-0109). The factory marks ready only a failed draft a
// writer's retry watches again, once GitHub says it can merge it, at the
// head whose gate passed.

// A modeled core's failed draft, which a writer's retry watches again, waits
// for a person once CI's gate passes: the factory marks it neither ready nor
// merged, and the retry's answer says a person merges it.
func TestACoresWatchedDraftWaitsForAPerson(t *testing.T) {
	r, g := draftRig(t)
	r.f.Projects = "factory"
	r.build.pass = false
	failed := ratified(t, r)
	n := failed.Marker.PR
	if !strings.HasPrefix(failed.Marker.Project, "factory/") || failed.Marker.Kind != KindFailed || n == 0 || !r.gh.prs[n].Draft {
		t.Fatalf("a core's build that fails the gate should leave a draft: %+v", failed.Marker)
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	answer := r.expect(1, KindPR, LabelPR)
	if said := retry263Visible(answer.Comment.Body); !strings.Contains(said, "a person merges it") || strings.Contains(said, "I'll merge it") {
		t.Errorf("the retry's answer must leave the merge to a person:\n%s", said)
	}
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on the watched draft")
	waiting := r.expect(1, KindFailed, LabelHumanReview)
	if waiting.Marker.Why() != FailTrusted || len(g.calls) != 0 || len(r.gh.merged) != 0 || !r.gh.prs[n].Draft {
		t.Fatalf("a core's watched draft must wait for a person, unmarked and unmerged: %+v; asked GitHub for %v, merged %v", waiting.Marker, g.calls, r.gh.merged)
	}
}

// A modeled core's build that passes opens its pull request saying a person
// merges it, and once CI's gate passes, it waits for that person, then
// records the merge the person makes as theirs.
func TestACoresPullRequestWaitsForAPerson(t *testing.T) {
	r, g := draftRig(t)
	r.f.Projects = "factory"
	built := ratified(t, r)
	n := built.Marker.PR
	if built.Marker.Kind != KindPR || !strings.Contains(built.Comment.Body, "a person merges it") || !strings.Contains(r.gh.bodies[n], "Trusted base") {
		t.Fatalf("a core's pull request must say a person merges it:\n%s\n\n%s", built.Comment.Body, r.gh.bodies[n])
	}
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed")
	if waiting := r.expect(1, KindFailed, LabelHumanReview); waiting.Marker.Why() != FailTrusted || len(g.calls) != 0 || len(r.gh.merged) != 0 {
		t.Fatalf("a core's pull request must wait for a person: %+v; asked GitHub for %v, merged %v", waiting.Marker, g.calls, r.gh.merged)
	}
	pr := r.gh.prs[n]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "fedcba9merge"
	pollOK(t, r, fmt.Sprintf("after a person merged #%d", n))
	merged := r.expect(1, KindMerged, LabelMerged)
	if said := retry263Visible(merged.Comment.Body); !strings.Contains(said, fmt.Sprintf("#%d was merged", n)) || strings.Contains(said, "I merged") {
		t.Errorf("the merge is the person's:\n%s", said)
	}
}

// A draft no writer's retry watches, as when a person made the factory's
// pull request a draft to stop it, is theirs: once CI's gate passes, the
// factory says once that it won't mark it ready, asks GitHub for nothing,
// and hears commands again, so a writer's retry has it watch the draft and
// merge it after all (#369's review).
func TestADraftAPersonMadeOfTheFactorysPullRequestStaysTheirs(t *testing.T) {
	r, g := draftRig(t)
	n := ratified(t, r).Marker.PR
	r.expect(1, KindPR, LabelPR)
	r.gh.prs[n].Draft = true
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on a draft a person made")
	said := r.expect(1, KindFailed, LabelHumanReview)
	if said.Marker.Why() != FailDraft || !strings.Contains(retry263Visible(said.Comment.Body), fmt.Sprintf("close #%d or this issue", n)) {
		t.Fatalf("the factory must say once that it won't mark the draft ready: %+v\n%s", said.Marker, said.Comment.Body)
	}
	posts := len(r.gh.posts(1))
	for i := 1; i <= 3; i++ {
		pollOK(t, r, fmt.Sprintf("%d polls after it said so", i))
	}
	if len(r.gh.posts(1)) != posts || len(g.calls) != 0 || len(r.gh.merged) != 0 || !r.gh.prs[n].Draft {
		t.Fatalf("a person's draft must stay theirs: %d more posts, asked GitHub for %v, merged %v", len(r.gh.posts(1))-posts, g.calls, r.gh.merged)
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindPR, LabelPR)
	pollOK(t, r, "in the poll after the retry's answer")
	r.expect(1, KindMerged, LabelMerged)
	if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls, want) {
		t.Errorf("the factory asked GitHub for %v; want %v", g.calls, want)
	}
}

// A watched draft stays watched when the retry that watched it is gone, as
// when its writer deleted it or lost write access: the factory's own answer
// says so (#369's review).
func TestAWatchedDraftOutlivesItsRetry(t *testing.T) {
	r, g, n := watchedDraft(t)
	kept := r.gh.comments[1][:0]
	for _, c := range r.gh.comments[1] {
		if c.Body != "/invariant retry" {
			kept = append(kept, c)
		}
	}
	r.gh.comments[1] = kept
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on a draft whose retry is gone")
	r.expect(1, KindMerged, LabelMerged)
	if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls, want) {
		t.Errorf("the factory asked GitHub for %v; want %v", g.calls, want)
	}
}

// watchedDraft has a writer retry a failed build's draft, which the factory
// watches again, and returns the draft's number.
func watchedDraft(t *testing.T) (*rig, *draftGitHub, int) {
	t.Helper()
	r, g := draftRig(t)
	r.build.pass = false
	n := ratified(t, r).Marker.PR
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindPR, LabelPR)
	return r, g, n
}

// A watched draft GitHub says it can't merge is reported as one GitHub
// refused, without marking it ready, since the core's model never marks
// ready a draft the factory can't merge.
func TestAWatchedDraftGitHubCantMergeIsReportedUnmarked(t *testing.T) {
	r, g, n := watchedDraft(t)
	no := false
	r.gh.prs[n].Mergeable = &no
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on a draft GitHub can't merge")
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if failed.Marker.Why() != FailMerge || len(g.calls) != 0 || !r.gh.prs[n].Draft || !strings.Contains(failed.Comment.Body, "can't merge it") {
		t.Fatalf("the draft must be reported unmarked: %+v; asked GitHub for %v\n%s", failed.Marker, g.calls, failed.Comment.Body)
	}
}

// Until GitHub has worked out whether it can merge a watched draft, the
// factory takes no step, and it marks the draft ready and merges it once
// GitHub says it can.
func TestAWatchedDraftWaitsForGitHubToSayItCanMerge(t *testing.T) {
	r, g, n := watchedDraft(t)
	logs := &logLines{t: t}
	r.f.Log = logs.add
	r.gh.prs[n].Mergeable = nil
	r.gh.ci(n, "success")
	posts := len(r.gh.posts(1))
	pollOK(t, r, "while GitHub hasn't said whether it can merge the draft")
	pollOK(t, r, "while GitHub still hasn't")
	if len(g.calls) != 0 || len(r.gh.posts(1)) != posts {
		t.Fatalf("the factory must wait for GitHub: asked it for %v, and posted %d times", g.calls, len(r.gh.posts(1))-posts)
	}
	waits := 0
	for _, line := range logs.all() {
		if strings.Contains(line, "waiting for GitHub to say whether it can merge") {
			waits++
		}
	}
	if waits != 1 {
		t.Errorf("the factory must say once that it waits for GitHub; it said so %d times", waits)
	}
	yes := true
	r.gh.prs[n].Mergeable = &yes
	pollOK(t, r, "once GitHub says it can merge the draft")
	r.expect(1, KindMerged, LabelMerged)
	if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls, want) {
		t.Errorf("the factory asked GitHub for %v; want %v", g.calls, want)
	}
}

// pushOnMerge pushes a commit to branch in origin once the factory records
// a merge, after it checked the head and before it marks the draft ready.
type pushOnMerge struct {
	Repo
	t              *testing.T
	origin, branch string
	pushed         *bool
}

func (p pushOnMerge) Record(ctx context.Context, issue int, step, what string) error {
	err := p.Repo.Record(ctx, issue, step, what)
	if strings.HasPrefix(step, "merge-") && !*p.pushed {
		*p.pushed = true
		head := git(p.t, p.origin, "rev-parse", "refs/heads/"+p.branch)
		next := git(p.t, p.origin, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit-tree", head+"^{tree}", "-p", head, "-m", "a push")
		git(p.t, p.origin, "update-ref", "refs/heads/"+p.branch, next)
	}
	return err
}

// A push to a watched draft after the factory checked its head, before it
// marks the draft ready, leaves the draft as it is: CI's gate never passed
// on the new head.
func TestAPushBeforeTheMarkReadyLeavesTheDraft(t *testing.T) {
	r, g, n := watchedDraft(t)
	pushed := false
	r.f.Repo = pushOnMerge{Repo: r.f.Repo, t: t, origin: r.origin, branch: r.gh.prs[n].Head.Ref, pushed: &pushed}
	r.gh.ci(n, "success")
	pollOK(t, r, "when someone pushes to the draft as the factory merges it")
	pollOK(t, r, "on the next poll, with no gate on the new head")
	if !pushed || len(g.calls) != 0 || !r.gh.prs[n].Draft || len(r.gh.merged) != 0 {
		t.Fatalf("a head CI never checked must not be marked ready: pushed %v, asked GitHub for %v, merged %v", pushed, g.calls, r.gh.merged)
	}
}

// The build's post maps its pull request onto the core as GitHub has it:
// open as a draft or ready, or closed or merged by someone else, and the
// core lets the build say so in each.
func TestABuildsPullRequestMapsAsGitHubHasIt(t *testing.T) {
	f := &Factory{}
	for _, c := range []struct {
		pr   github.PullRequest
		want int
	}{
		{github.PullRequest{State: "open", Draft: true}, recovery.PRDraft},
		{github.PullRequest{State: "open"}, recovery.PRReady},
		{github.PullRequest{State: "closed"}, recovery.PRClosed},
		{github.PullRequest{State: "closed", Merged: true}, recovery.PRMerged},
	} {
		s := openedAs(c.pr)
		if r := f.repo(s); r.PR != c.want || !r.Opened || r.Merged || !f.canPost(s, recovery.Build) {
			t.Errorf("%+v maps as %d, opened %v, merged by the factory %v; want %d, and the build's post allowed", c.pr, r.PR, r.Opened, r.Merged, c.want)
		}
	}
}

// readyPush pushes a commit to branch in origin as the factory marks the
// draft ready, and records the head each merge asks for, refusing one that
// isn't the pull request's head, as GitHub does.
type readyPush struct {
	*draftGitHub
	t              *testing.T
	origin, branch string
	pushed         string
	asked          []string
}

func (g *readyPush) MarkReady(ctx context.Context, n int) error {
	head := git(g.t, g.origin, "rev-parse", "refs/heads/"+g.branch)
	g.pushed = git(g.t, g.origin, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit-tree", head+"^{tree}", "-p", head, "-m", "a push")
	git(g.t, g.origin, "update-ref", "refs/heads/"+g.branch, g.pushed)
	return g.draftGitHub.MarkReady(ctx, n)
}

func (g *readyPush) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.asked = append(g.asked, sha)
	if now := git(g.t, g.origin, "rev-parse", "refs/heads/"+g.branch); sha != now {
		return "", fmt.Errorf("PUT repos/o/r/pulls/%d/merge: exit status 1: gh: Head branch was modified. Review and try the merge again. (HTTP 409)", n)
	}
	return g.draftGitHub.Merge(ctx, n, sha, method)
}

// A push to a watched draft while the factory marks it ready isn't merged:
// the merge is pinned to the head whose gate, scope, lock and trusted files
// the factory checked, and a head that moved waits for CI on it (#369's
// review).
func TestAPushWhileTheDraftIsMarkedReadyIsntMerged(t *testing.T) {
	r, g, n := watchedDraft(t)
	gated := r.gh.prs[n].Head.SHA
	p := &readyPush{draftGitHub: g, t: t, origin: r.origin, branch: r.gh.prs[n].Head.Ref}
	r.f.GitHub = p
	r.gh.ci(n, "success")
	pollOK(t, r, "when someone pushes to the draft as the factory marks it ready")
	pollOK(t, r, "on the next poll, with no gate on the new head")
	for _, sha := range p.asked {
		if sha != gated {
			t.Errorf("the factory asked to merge %s, which CI never passed; its gate passed on %s", sha, gated)
		}
	}
	if p.pushed == "" || len(r.gh.merged) != 0 {
		t.Fatalf("a head CI never checked must not merge: pushed %q, merged %v", p.pushed, r.gh.merged)
	}
}

// A build that passed and finds a draft open from its branch that GitHub
// refuses to mark ready says so, as a failure that names the draft, so a
// writer's retry watches it again, and it merges once CI's gate passes
// (#369's review).
func TestAPublishedDraftGitHubWontMarkReadyAsksForARetry(t *testing.T) {
	r, g, left := publishedDraft(t)
	g.unready = "Pull request could not be marked ready for review (HTTP 422)"
	pollOK(t, r, "in the build, when GitHub refuses to mark the draft ready")
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if failed.Marker.Why() != FailUnready || failed.Marker.PR != left || failed.Marker.Rebuilds() || !strings.Contains(failed.Comment.Body, fmt.Sprintf("watch #%d again", left)) {
		t.Fatalf("the build must say GitHub refused, naming draft #%d for a retry to watch: %+v\n%s", left, failed.Marker, failed.Comment.Body)
	}
	g.unready = ""
	r.gh.ci(left, "success")
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindPR, LabelPR)
	pollOK(t, r, "in the poll after the retry's answer")
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{left}) {
		t.Errorf("merged %v; want #%d", r.gh.merged, left)
	}
}

// GitHub failing for a moment as the build marks the draft ready isn't a
// refusal: the next poll tries again, and the build goes on as it would
// have (#369's review).
func TestAPublishedDraftGitHubFailsToMarkReadyIsTriedAgain(t *testing.T) {
	r, g, left := publishedDraft(t)
	g.unready = "Bad Gateway (HTTP 502)"
	if err := r.f.Poll(context.Background()); err == nil {
		t.Fatal("the build must fail its step, to try again, when GitHub fails for a moment")
	}
	r.expect(1, KindRatified, LabelBuilding)
	g.unready = ""
	pollOK(t, r, "on the next poll")
	if built := r.expect(1, KindPR, LabelPR); built.Marker.PR != left || r.gh.prs[left].Draft {
		t.Fatalf("the build must take #%d, ready: %+v", left, built.Marker)
	}
}

// publishedDraft has a build stop, a person open a draft from its branch,
// and a writer retry, so the next poll's build, which passes, finds that
// draft open, as in #145's test.
func publishedDraft(t *testing.T) (*rig, *draftGitHub, int) {
	t.Helper()
	r, g := draftRig(t)
	r.build.stop = true
	stopped := ratified(t, r)
	left, err := r.gh.CreatePullRequest(context.Background(), github.NewPullRequest{
		Title: "Add a bounded buffer", Head: stopped.Marker.Branch, Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	r.build.stop = false
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindRatified, LabelBuilding)
	return r, g, left.Number
}
