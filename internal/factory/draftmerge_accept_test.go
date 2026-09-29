package factory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// GitHub won't merge a draft pull request. On copythis-ad#36, CI's gate had
// passed on the factory's draft #38 and a writer had said retry, and the
// factory tried to merge it every 30 seconds for seven minutes, told each
// time "Pull Request is still a draft (HTTP 405)", until a person marked it
// ready (#145). The factory marks a draft ready for review once the protocol
// allows the merge, and merges it in the same poll. A merge GitHub refuses
// is said on the issue once, and waits for a writer's retry.

// draftGitHub is GitHub as it treats drafts: it refuses to merge a draft,
// and marks a pull request ready for review when asked. With refuse set, it
// refuses every merge with that answer, and with unready set, every request
// to mark a pull request ready. calls lists what the factory asked of it, in
// order.
type draftGitHub struct {
	*fakeGitHub
	refuse, unready string
	calls           []string
}

func (g *draftGitHub) MarkReady(_ context.Context, n int) error {
	g.calls = append(g.calls, fmt.Sprintf("ready #%d", n))
	pr, ok := g.prs[n]
	switch {
	case !ok:
		return github.ErrNotFound
	case g.unready != "":
		return fmt.Errorf("POST graphql: exit status 1: gh: %s", g.unready)
	}
	pr.Draft = false
	return nil
}

func (g *draftGitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.calls = append(g.calls, fmt.Sprintf("merge #%d", n))
	why := g.refuse
	if pr, ok := g.prs[n]; ok && pr.Draft {
		why = "Pull Request is still a draft (HTTP 405)"
	}
	if why != "" {
		return "", fmt.Errorf("PUT repos/o/r/pulls/%d/merge: exit status 1: gh: %s", n, why)
	}
	return g.fakeGitHub.Merge(ctx, n, sha, method)
}

// asked counts the times the factory asked GitHub for call, such as
// "merge #101".
func (g *draftGitHub) asked(call string) int {
	n := 0
	for _, c := range g.calls {
		if c == call {
			n++
		}
	}
	return n
}

// draftRig is a rig whose GitHub treats drafts as GitHub does.
func draftRig(t *testing.T) (*rig, *draftGitHub) {
	t.Helper()
	r := newRig(t)
	g := &draftGitHub{fakeGitHub: r.gh}
	r.f.GitHub = g
	return r, g
}

// pollOK polls once, and stops the test if the poll fails, saying when.
func pollOK(t *testing.T, r *rig, when string) {
	t.Helper()
	if err := r.f.Poll(context.Background()); err != nil {
		t.Fatalf("%s, the poll failed: %v", when, err)
	}
}

// The draft of a build that failed the gate, whose gate then passed on CI,
// and whose retry a writer asked for, as #38's was: the factory marks it
// ready for review and merges it, in the one poll after it answers the
// retry. It asks nothing of the draft before the protocol allows the merge.
func TestADraftWhoseGatePassedAfterARetryIsMarkedReadyAndMerged(t *testing.T) {
	r, g := draftRig(t)
	r.build.pass = false
	failed := ratified(t, r)
	n := failed.Marker.PR
	if failed.Marker.Kind != KindFailed || n == 0 || !r.gh.prs[n].Draft {
		t.Fatalf("a build that fails the gate should leave a draft pull request: %+v", failed.Marker)
	}
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on the draft")
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindPR, LabelPR)
	if len(g.calls) != 0 {
		t.Fatalf("before the protocol allowed a merge, the factory asked GitHub for %v", g.calls)
	}

	pollOK(t, r, fmt.Sprintf("in the poll after the retry's answer, which should mark draft #%d ready for review and merge it", n))
	merged := r.expect(1, KindMerged, LabelMerged)
	if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls, want) {
		t.Errorf("the factory asked GitHub for %v; want %v", g.calls, want)
	}
	if r.gh.prs[n].Draft || !reflect.DeepEqual(r.gh.merged, []int{n}) {
		t.Errorf("#%d: draft %v, merged %v; want it ready and merged", n, r.gh.prs[n].Draft, r.gh.merged)
	}
	if !strings.Contains(merged.Comment.Body, "I merged it as abc123merge") {
		t.Errorf("the post doesn't say the factory merged it:\n%s", merged.Comment.Body)
	}
}

// A merge GitHub refuses, or a draft it won't mark ready for the merge, is
// said on the issue once: a failed post, for a person to look at, that names
// the pull request and quotes GitHub's answer. The factory tries nothing
// again on the polls after, and tries again once a writer says retry.
func TestAMergeGitHubRefusesIsSaidOnce(t *testing.T) {
	for _, c := range []struct {
		name  string
		draft bool   // the pull request is the draft of a build that failed the gate
		why   string // GitHub's answer
	}{
		{"the merge", false, "Pull Request is not mergeable (HTTP 405)"},
		{"marking the draft ready", true, "Pull request could not be marked ready for review (HTTP 422)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, g := draftRig(t)
			r.build.pass = !c.draft
			n := ratified(t, r).Marker.PR
			first := fmt.Sprintf("merge #%d", n)
			if c.draft {
				r.gh.say(1, "gitdek", "/invariant retry")
				pollOK(t, r, "after a writer said retry")
				g.unready, first = c.why, fmt.Sprintf("ready #%d", n)
			} else {
				g.refuse = c.why
			}
			r.expect(1, KindPR, LabelPR)
			r.gh.ci(n, "success")
			pollOK(t, r, fmt.Sprintf("when GitHub refused %s of #%d, which should be said on the issue", c.name, n))
			failed := r.expect(1, KindFailed, LabelHumanReview)
			said, _, _ := strings.Cut(failed.Comment.Body, "<!-- invariant:")
			for _, want := range []string{fmt.Sprintf("#%d", n), c.why, "`/invariant retry`"} {
				if !strings.Contains(said, want) {
					t.Errorf("the post doesn't say %q:\n%s", want, said)
				}
			}
			if failed.Marker.PR != n || failed.Marker.Why() != "merge" {
				t.Errorf("the post records #%d, failed with %q; want #%d, failed with %q", failed.Marker.PR, failed.Marker.Why(), n, "merge")
			}
			if want := []string{first}; !reflect.DeepEqual(g.calls, want) || len(r.gh.merged) != 0 {
				t.Errorf("the factory asked GitHub for %v, and merged %v; want only %v", g.calls, r.gh.merged, want)
			}

			posts, calls := len(r.gh.posts(1)), len(g.calls)
			for i := 1; i <= 3; i++ {
				pollOK(t, r, fmt.Sprintf("%d polls after the refusal was said", i))
			}
			if got := len(r.gh.posts(1)); got != posts {
				t.Errorf("the factory posted %d more times after it said GitHub refused", got-posts)
			}
			if len(g.calls) != calls {
				t.Errorf("the factory tried again on its own: it asked GitHub for %v", g.calls[calls:])
			}

			// Once the cause is fixed, a writer's retry has it try again.
			g.refuse, g.unready = "", ""
			r.gh.say(1, "gitdek", "/invariant retry")
			pollOK(t, r, "after a writer said retry once the cause was fixed")
			r.expect(1, KindPR, LabelPR)
			pollOK(t, r, "in the poll after the second retry's answer")
			r.expect(1, KindMerged, LabelMerged)
			if !reflect.DeepEqual(r.gh.merged, []int{n}) || r.gh.prs[n].Draft {
				t.Errorf("#%d: draft %v, merged %v; want it merged once a writer said retry", n, r.gh.prs[n].Draft, r.gh.merged)
			}
		})
	}
}

// Once the factory has said GitHub refused its merge, a person may merge the
// pull request on GitHub. The factory records that it was merged, not as its
// own merge, though it recorded its own attempt at that head before GitHub
// refused it.
func TestAPersonMergesWhatGitHubRefusedTheFactory(t *testing.T) {
	r, g := draftRig(t)
	n := ratified(t, r).Marker.PR
	g.refuse = "Base branch was modified. Review and try the merge again. (HTTP 405)"
	r.gh.ci(n, "success")
	pollOK(t, r, fmt.Sprintf("when GitHub refused to merge #%d, which should be said on the issue", n))
	r.expect(1, KindFailed, LabelHumanReview)

	pr := r.gh.prs[n]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "fedcba9merge"
	pollOK(t, r, fmt.Sprintf("after a person merged #%d", n))
	merged := r.expect(1, KindMerged, LabelMerged)
	said, _, _ := strings.Cut(merged.Comment.Body, "<!-- invariant:")
	if !strings.Contains(said, fmt.Sprintf("#%d was merged", n)) || strings.Contains(said, "I merged") {
		t.Errorf("the post should say #%d was merged, and not by the factory:\n%s", n, said)
	}
	if got := g.asked(fmt.Sprintf("merge #%d", n)); got != 1 {
		t.Errorf("the factory asked GitHub to merge #%d %d times; want once, the time GitHub refused", n, got)
	}
}

// A build that passed the gate, and finds a draft already open from its
// branch, which was opened without this build's result, makes it the pull
// request its result says: it marks it ready for review before it says the
// pull request is open, and opens no other. It merges once CI's gate
// passes.
func TestABuildThatPassedMarksADraftOpenFromItsBranchReady(t *testing.T) {
	r, _ := draftRig(t)
	r.build.stop = true
	stopped := ratified(t, r)
	if stopped.Marker.Kind != KindFailed || stopped.Marker.Why() != FailStopped || stopped.Marker.Branch == "" {
		t.Fatalf("the first build should stop before it opens a pull request: %+v", stopped.Marker)
	}
	left, err := r.gh.CreatePullRequest(context.Background(), github.NewPullRequest{
		Title: "Add a bounded buffer", Head: stopped.Marker.Branch, Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}

	r.build.stop = false
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindRatified, LabelBuilding)
	pollOK(t, r, "in the build")
	built := r.expect(1, KindPR, LabelPR)
	if built.Marker.PR != left.Number || len(r.gh.prs) != 1 {
		t.Fatalf("the build should take #%d, open from its branch: it names #%d, among %d pull requests", left.Number, built.Marker.PR, len(r.gh.prs))
	}
	if r.gh.prs[left.Number].Draft {
		t.Fatalf("the build passed the gate, and says so, but #%d is still a draft:\n%s", left.Number, built.Comment.Body)
	}

	r.gh.ci(left.Number, "success")
	pollOK(t, r, "after CI's gate passed")
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{left.Number}) {
		t.Errorf("merged %v; want #%d", r.gh.merged, left.Number)
	}
}
