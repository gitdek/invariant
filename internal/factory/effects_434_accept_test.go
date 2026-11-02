package factory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// A definitive refusal holds the issue, including effects outside PR
// creation. The tests observe the public poll, comments and remote effects;
// they do not prescribe a new protocol state or a failure-marker encoding.
type effects434GitHub struct {
	GitHub
	op             string
	calls          int
	message        string
	reportFailures int
	allIssues      *fakeGitHub
	closeOnMerge   bool
}

func (g *effects434GitHub) Issues(_ context.Context, label string) ([]github.Issue, error) {
	var out []github.Issue
	for _, issue := range g.allIssues.issues {
		if issue.PullRequest == nil && (label == "" || issue.HasLabel(label)) {
			out = append(out, *issue)
		}
	}
	return out, nil
}

func (g *effects434GitHub) attempt(op string) error {
	if op != g.op {
		return nil
	}
	g.calls++
	if g.calls == 1 {
		return fmt.Errorf("%s: exit status 1: gh: %s (HTTP 422)", op, g.message)
	}
	return nil
}

func (g *effects434GitHub) CreateIssue(ctx context.Context, is github.NewIssue) (github.Issue, error) {
	if err := g.attempt("create issue"); err != nil {
		return github.Issue{}, err
	}
	return g.GitHub.CreateIssue(ctx, is)
}

func (g *effects434GitHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	if err := g.attempt("create pull request"); err != nil {
		return github.PullRequest{}, err
	}
	return g.GitHub.CreatePullRequest(ctx, pr)
}

func (g *effects434GitHub) ClosePullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	if err := g.attempt("close pull request"); err != nil {
		return github.PullRequest{}, err
	}
	return g.GitHub.ClosePullRequest(ctx, n)
}

func (g *effects434GitHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if strings.Contains(body, g.message) {
		if g.reportFailures > 0 {
			g.reportFailures--
			return github.Comment{}, fmt.Errorf("POST issue comment: gh: Service Unavailable (HTTP 503)")
		}
	} else if m, ok := DecodeMarker(body); ok && m.Kind == KindProposal {
		if err := g.attempt("post proposal"); err != nil {
			return github.Comment{}, err
		}
	}
	return g.GitHub.PostComment(ctx, n, body)
}

func (g *effects434GitHub) AddLabels(ctx context.Context, n int, labels ...string) error {
	for _, label := range labels {
		if label == LabelProposal {
			if err := g.attempt("add label"); err != nil {
				return err
			}
		}
	}
	return g.GitHub.AddLabels(ctx, n, labels...)
}

func (g *effects434GitHub) RemoveLabel(ctx context.Context, n int, label string) error {
	if label == LabelAsking {
		if err := g.attempt("remove label"); err != nil {
			return err
		}
	}
	return g.GitHub.RemoveLabel(ctx, n, label)
}

func (g *effects434GitHub) DeleteBranch(ctx context.Context, branch string) error {
	if err := g.attempt("delete branch"); err != nil {
		return err
	}
	return g.GitHub.DeleteBranch(ctx, branch)
}

func (g *effects434GitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	merged, err := g.GitHub.Merge(ctx, n, sha, method)
	if err == nil && g.closeOnMerge {
		// GitHub applies the PR's Closes #1 before the next issue poll.
		g.allIssues.issues[1].State = "closed"
	}
	return merged, err
}

func effects434Poll(t *testing.T, r *rig) {
	t.Helper()
	// A handled refusal may still be returned for the operator's log. The
	// observable contract is the report and durable hold, not Poll's error.
	if err := r.f.Poll(context.Background()); err != nil {
		t.Logf("poll: %v", err)
	}
}

func effects434Restart(r *rig, gh GitHub) {
	plumbing := r.f.Plumbing
	r.f = freshWatcher(r, gh)
	r.f.Plumbing = plumbing
}

func effects434Reports(r *rig, issue int, message string) []github.Comment {
	var found []github.Comment
	for _, c := range r.gh.comments[issue] {
		if strings.Contains(c.Body, message) {
			found = append(found, c)
		}
	}
	return found
}

func effects434ExpectReport(t *testing.T, r *rig, issue int, g *effects434GitHub) {
	t.Helper()
	posts := effects434Reports(r, issue, g.message)
	if len(posts) != 1 {
		t.Fatalf("%s refusal must be posted once on #%d with GitHub's message; got %d matching posts", g.op, issue, len(posts))
	}
	if !strings.Contains(posts[0].Body, "/invariant retry") {
		t.Errorf("refusal must explain how a writer resumes it: %s", posts[0].Body)
	}
	if !r.gh.issues[issue].HasLabel(LabelHumanReview) {
		t.Errorf("%s refusal must label originating issue #%d for a person: %v", g.op, issue, r.gh.labelsOf(issue))
	}
}

func TestOtherPermanentEffects434WaitForAWriter(t *testing.T) {
	for _, op := range []string{"create issue", "close pull request", "post proposal", "add label", "remove label", "delete branch"} {
		t.Run(op, func(t *testing.T) {
			var r *rig
			issue := 1
			var completed func() bool
			switch op {
			case "create issue":
				var hub *planHub
				r, hub = pipelineRig(t)
				ratifyPipeline(t, r, draftPipeline(t, r))
				issue = pipelinePRD
				completed = func() bool { return len(hub.opened) == 1 }
			case "close pull request":
				var failed Post
				r, _, failed = rebuild271Start(t, 1, "page/page.go")
				pr := failed.Marker.PR
				r.gh.say(issue, "gitdek", "/invariant retry")
				completed = func() bool { return r.gh.prs[pr].State == "closed" && !r.gh.prs[pr].Merged }
			case "delete branch":
				r = newRig(t)
				pr := ratified(t, r)
				r.gh.ci(pr.Marker.PR, "success")
				builds := len(r.build.built)
				completed = func() bool {
					mergedPosts := 0
					for _, p := range r.gh.posts(issue) {
						if p.Marker.Kind == KindMerged {
							mergedPosts++
						}
					}
					return len(r.gh.deleted) == 1 && len(r.gh.merged) == 1 && len(r.build.built) == builds && mergedPosts == 1
				}
			default:
				r = newRig(t)
				r.form.forks = nil
				r.gh.open(issue, "gitdek", "A bounded buffer", "Build the bounded buffer.\n\n/invariant solve", LabelAsking)
				completed = func() bool {
					for _, p := range r.gh.posts(issue) {
						if p.Marker.Kind == KindProposal && r.gh.issues[issue].HasLabel(LabelProposal) && !r.gh.issues[issue].HasLabel(LabelAsking) {
							return true
						}
					}
					return false
				}
			}
			g := &effects434GitHub{GitHub: r.f.GitHub, op: op, message: "fixture refuses " + op, allIssues: r.gh, closeOnMerge: op == "delete branch"}
			r.f.GitHub = g
			effects434Poll(t, r)
			if g.calls != 1 {
				t.Fatalf("must try %s exactly once before waiting; got %d calls", op, g.calls)
			}
			if op == "delete branch" && r.gh.issues[issue].State != "closed" {
				t.Fatal("fixture must close the issue when its PR merges, before cleanup is refused")
			}
			effects434ExpectReport(t, r, issue, g)
			effects434Restart(r, g)
			for i := 0; i < 3; i++ {
				effects434Poll(t, r)
			}
			r.gh.say(issue, "mallory", "/invariant retry")
			effects434Poll(t, r)
			if g.calls != 1 {
				t.Fatalf("polls, restart and a reader's retry must not repeat %s; got %d calls", op, g.calls)
			}
			effects434ExpectReport(t, r, issue, g)
			r.gh.say(issue, "gitdek", "/invariant retry")
			for i := 0; i < 4 && (g.calls == 1 || !completed()); i++ {
				effects434Poll(t, r)
			}
			if g.calls < 2 || !completed() {
				t.Fatalf("a writer's retry must resume %s and complete its original work; calls=%d, completed=%v", op, g.calls, completed())
			}
			if n := len(effects434Reports(r, issue, g.message)); n != 1 {
				t.Errorf("the original refusal was posted %d times after retry; want once", n)
			}
		})
	}
}

// Reporting a refusal can itself lose GitHub for a moment. The refused
// business effect stays stopped across a restart while its report is retried.
func TestPermanentEffect434SurvivesAReportFailure(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.gh.open(1, "gitdek", "A bounded buffer", "Build the bounded buffer.\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	g := &effects434GitHub{GitHub: r.f.GitHub, op: "create pull request", message: "fixture refuses create pull request", reportFailures: 1, allIssues: r.gh}
	r.f.GitHub = g
	effects434Poll(t, r)
	if g.calls != 1 {
		t.Fatalf("fixture must reach one refused PR creation: %d calls", g.calls)
	}
	effects434Restart(r, g)
	for i := 0; i < 3; i++ {
		effects434Poll(t, r)
	}
	if g.calls != 1 || len(r.gh.prs) != 0 || len(r.build.built) != 1 {
		t.Fatalf("report recovery must not repeat the refused effect or rebuild: creates=%d, PRs=%d, builds=%d", g.calls, len(r.gh.prs), len(r.build.built))
	}
	effects434ExpectReport(t, r, 1, g)
}
