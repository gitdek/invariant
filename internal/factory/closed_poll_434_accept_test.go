package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// closedPoll434GitHub exposes the same all-state issue read as the real
// github.Client. OpenIssues remains the existing fake's open-only read, so
// these scenarios also catch a poll that never asks for closed issues.
type closedPoll434GitHub struct {
	*fakeGitHub
	failCleanup bool
}

func (g *closedPoll434GitHub) Issues(_ context.Context, label string) ([]github.Issue, error) {
	var out []github.Issue
	for _, issue := range g.issues {
		if issue.PullRequest == nil && (label == "" || issue.HasLabel(label)) {
			out = append(out, *issue)
		}
	}
	return out, nil
}

var errClosedPoll434Cleanup = errors.New("the label cleanup lost its connection")

func (g *closedPoll434GitHub) RemoveLabel(ctx context.Context, n int, label string) error {
	if g.failCleanup && g.issues[n] != nil && g.issues[n].State == "closed" {
		g.failCleanup = false
		return errClosedPoll434Cleanup
	}
	return g.fakeGitHub.RemoveLabel(ctx, n, label)
}

func closedPoll434Hub(r *rig) *closedPoll434GitHub {
	g := &closedPoll434GitHub{fakeGitHub: r.gh}
	r.f.GitHub = g
	return g
}

// A final record must clear every stale active state, while keeping the
// issue's unrelated labels. A terminal merged label is allowed either way.
func closedPoll434NoActiveLabels(t *testing.T, r *rig, n int) {
	t.Helper()
	for _, label := range []string{LabelAsking, LabelProposal, LabelBuilding, LabelPR, LabelHumanReview} {
		if r.gh.issues[n].HasLabel(label) {
			t.Errorf("closed #%d still has active state label %q", n, label)
		}
	}
	for _, label := range []string{LabelTrigger, "bug"} {
		if !r.gh.issues[n].HasLabel(label) {
			t.Errorf("closed #%d lost unrelated label %q", n, label)
		}
	}
}

func closedPoll434PR(t *testing.T, trusted bool) (*rig, Post) {
	t.Helper()
	file := "page/page.go"
	if trusted {
		file = "internal/factory/page.go"
	}
	r, _ := plumbingRig(t, file)
	closedPoll434Hub(r)
	r.gh.open(1, "gitdek", "A hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	pr := r.expect(1, KindPR, LabelPR)
	if trusted {
		r.gh.ci(pr.Marker.PR, "success")
		r.poll()
		waiting := r.expect(1, KindFailed, LabelHumanReview)
		if waiting.Marker.Failure != FailTrusted {
			t.Fatalf("the trusted pull request should wait for a person: %+v", waiting.Marker)
		}
	}
	if err := r.gh.AddLabels(context.Background(), 1, LabelTrigger, "bug"); err != nil {
		t.Fatal(err)
	}
	return r, pr
}

// GitHub's Closes #N closes the issue before the watcher next polls. Both
// a watched PR and one already waiting for a person's merge need a record.
func TestClosedPoll434RecordsHumanMergeAfterIssueAutoCloses(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			r, opened := closedPoll434PR(t, trusted)
			pr := r.gh.prs[opened.Marker.PR]
			pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-merge-434"
			r.gh.issues[1].State = "closed"
			before := len(r.gh.posts(1))
			r.poll()
			last := r.gh.last(1)
			if last.Marker.Kind != KindMerged || last.Marker.PR != pr.Number || !strings.Contains(last.Comment.Body, fmt.Sprintf("#%d", pr.Number)) || !strings.Contains(last.Comment.Body, "was merged") {
				t.Fatalf("the next poll did not record the person's merge: %+v\n%s", last.Marker, last.Comment.Body)
			}
			if len(r.gh.posts(1)) != before+1 || len(r.gh.merged) != 0 || len(r.gh.closed) != 0 {
				t.Fatalf("the poll should record one external merge without performing one: posts=%d, merged=%v, closed=%v", len(r.gh.posts(1))-before, r.gh.merged, r.gh.closed)
			}
			closedPoll434NoActiveLabels(t, r, 1)
			r.poll()
			if len(r.gh.posts(1)) != before+1 {
				t.Fatal("another poll repeated the closed issue's merge record")
			}
		})
	}
}

// Closing a started issue ends work even when its PR is still open and
// passing. A final close record must not claim that the open PR was closed.
func TestClosedPoll434RecordsIssueCloseWithoutMergingOpenPR(t *testing.T) {
	r, opened := closedPoll434PR(t, false)
	r.gh.ci(opened.Marker.PR, "success")
	r.gh.issues[1].State = "closed"
	r.gh.say(1, "gitdek", "/invariant retry")
	before := len(r.gh.posts(1))
	r.poll()
	last := r.gh.last(1)
	if !strings.Contains(strings.ToLower(last.Comment.Body), "issue") || !strings.Contains(strings.ToLower(last.Comment.Body), "closed") {
		t.Fatalf("the closed issue needs an issue-close record: %+v\n%s", last.Marker, last.Comment.Body)
	}
	pr := r.gh.prs[opened.Marker.PR]
	if pr.Merged || pr.State != "open" || len(r.gh.merged) != 0 || len(r.gh.closed) != 0 {
		t.Fatalf("closing the issue must stop work on its open PR: %+v", pr)
	}
	closedPoll434NoActiveLabels(t, r, 1)
	r.poll()
	if len(r.gh.posts(1)) != before+1 {
		t.Fatal("the closed issue was answered more than once")
	}
}

// A close before a PR exists also receives one final record, even when a
// ratify or retry is queued. No proposal, build or plan is resumed.
func TestClosedPoll434RecordsCloseBeforePullRequestExists(t *testing.T) {
	t.Run("untouched issues stay untouched", closedPoll434DoesNotStartUntouchedClosedIssues)
	for _, kind := range []string{KindForks, KindProposal, KindRatified, KindFailed} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t)
			closedPoll434Hub(r)
			r.gh.open(1, "gitdek", "Closed work", "/invariant solve", LabelTrigger, "bug", LabelBuilding, LabelHumanReview)
			plan := pipelinePlan()
			m := Marker{Kind: kind, Hash: plan.Hash(), Proposal: &formalize.Proposal{IssuePlan: plan, Hash: plan.Hash()}}
			if kind == KindForks {
				m.Forks = r.form.forks
			}
			r.gh.say(1, "gitdek", post("previous work", "Previous work.", m))
			r.gh.say(1, "gitdek", "/invariant retry\n/invariant ratify 0123456789ab")
			r.gh.issues[1].State = "closed"
			r.poll()
			last := r.gh.last(1)
			if last.Marker.PR != 0 || !strings.Contains(strings.ToLower(last.Comment.Body), "issue") || !strings.Contains(strings.ToLower(last.Comment.Body), "closed") {
				t.Fatalf("the close needs a final issue record without an invented PR: %+v\n%s", last.Marker, last.Comment.Body)
			}
			closedPoll434NoActiveLabels(t, r, 1)
			r.poll()
			if len(r.gh.posts(1)) != 2 || len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatalf("closed work must stay stopped: posts=%d, drafts=%d, builds=%d, PRs=%d", len(r.gh.posts(1)), len(r.form.requests), len(r.build.built), len(r.gh.prs))
			}
		})
	}
}

// Old final records can still carry active labels. Notes after a final
// record do not make it unfinished, and a completed issue plan is final too.
func TestClosedPoll434CleansTerminalHistoryWithoutAnotherPost(t *testing.T) {
	for _, kind := range []string{KindMerged, KindClosed, KindPlanDone} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t)
			closedPoll434Hub(r)
			r.gh.open(1, "gitdek", "Finished work", "", LabelTrigger, "bug", LabelAsking, LabelProposal, LabelBuilding, LabelPR, LabelHumanReview)
			previous := Marker{Kind: KindRatified}
			finished := Marker{Kind: kind}
			if kind == KindPlanDone {
				plan := pipelinePlan()
				previous.Hash = plan.Hash()
				previous.Proposal = &formalize.Proposal{IssuePlan: plan, Hash: plan.Hash()}
				finished.Hash = plan.Hash()
			}
			r.gh.say(1, "gitdek", post("previous work", "Previous work.", previous))
			r.gh.say(1, "gitdek", post("finished", "Finished.", finished))
			r.gh.say(1, "gitdek", post("note", "A note after the final record.", Marker{Kind: KindNote}))
			r.gh.issues[1].State = "closed"
			r.poll()
			closedPoll434NoActiveLabels(t, r, 1)
			r.poll()
			if len(r.gh.posts(1)) != 3 || len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatal("cleaning terminal history posted or resumed work")
			}
		})
	}
}

// Listing closed issues must not take new work from their old commands.
// This also protects the existing close-while-waiting-for-a-slot behavior.
func closedPoll434DoesNotStartUntouchedClosedIssues(t *testing.T) {
	r := newRig(t)
	closedPoll434Hub(r)
	for n, body := range []string{"Unrelated issue.", "/invariant solve", "/invariant plan"} {
		n++
		r.gh.open(n, "gitdek", "Already closed", body)
		r.gh.issues[n].State = "closed"
	}
	r.poll()
	for n := 1; n <= 3; n++ {
		if len(r.gh.posts(n)) != 0 {
			t.Errorf("untouched closed #%d got a factory post", n)
		}
	}
	if len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
		t.Fatal("the closed-issue scan started new work")
	}
}

// A successful final post followed by interrupted label cleanup must be
// recoverable from the thread alone, without duplicating the final post.
func TestClosedPoll434RetriesLabelCleanupWithoutRepeatingMergeRecord(t *testing.T) {
	r, opened := closedPoll434PR(t, false)
	g := r.f.GitHub.(*closedPoll434GitHub)
	pr := r.gh.prs[opened.Marker.PR]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-merge-434"
	r.gh.issues[1].State = "closed"
	g.failCleanup = true
	before := len(r.gh.posts(1))
	if err := r.f.Poll(context.Background()); !errors.Is(err, errClosedPoll434Cleanup) {
		t.Fatalf("the first cleanup should expose its interrupted request: %v", err)
	}
	if last := r.gh.last(1); last.Marker.Kind != KindMerged || len(r.gh.posts(1)) != before+1 {
		t.Fatalf("the final merge record should already be durable: %+v", last.Marker)
	}
	r.poll()
	closedPoll434NoActiveLabels(t, r, 1)
	if len(r.gh.posts(1)) != before+1 {
		t.Fatal("retrying cleanup repeated the durable merge record")
	}
}
