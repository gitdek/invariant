package factory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

type refusedReportLabel434GitHub struct {
	GitHub
	failures int
}

func TestRefusal434DirectWorkPlanRecoversAfterRestart(t *testing.T) {
	r, hub, plan := planRig(t, "Add the log buffer")
	g := &effects434GitHub{GitHub: hub, op: "create issue", message: "child creation refused", allIssues: r.gh}
	r.f.GitHub = g
	if err := r.f.WorkPlan(context.Background(), plan); err == nil {
		t.Fatal("the child issue's first creation should be refused")
	}
	r.f = freshWatcher(r, g)
	workOn(t, r.f, plan)
	if g.calls != 1 || len(hub.opened) != 0 {
		t.Fatal("a fresh direct caller repeated a refused child creation")
	}
	r.gh.say(plan.Issue, "gitdek", "/invariant retry")
	workOn(t, r.f, plan)
	if g.calls != 2 || len(hub.opened) != 1 || r.gh.issues[plan.Issue].HasLabel(LabelHumanReview) {
		t.Fatalf("direct retry did not finish: calls=%d opened=%v labels=%v", g.calls, hub.opened, r.gh.labelsOf(plan.Issue))
	}
}

func (g *refusedReportLabel434GitHub) AddLabels(ctx context.Context, n int, labels ...string) error {
	for _, label := range labels {
		if label == LabelHumanReview && g.failures > 0 {
			g.failures--
			return fmt.Errorf("POST issue labels: temporarily unavailable (HTTP 503)")
		}
	}
	return g.GitHub.AddLabels(ctx, n, labels...)
}

// Neither the report nor its label is needed to discover a refusal after
// restart. Recovering those writes must not retry the refused publication.
func TestRefusal434SurvivesMissingReportAndLabel(t *testing.T) {
	r, publication := publication434Proposal(t, false)
	const message = "publication refused while reporting was unavailable"
	g := &effects434GitHub{GitHub: publication, op: "create pull request", message: message, reportFailures: 1, allIssues: r.gh}
	hub := &refusedReportLabel434GitHub{GitHub: g, failures: 1}
	r.f.GitHub = hub
	effects434Poll(t, r)
	if g.calls != 1 || len(effects434Reports(r, 1, message)) != 0 || r.gh.issues[1].HasLabel(LabelHumanReview) {
		t.Fatalf("fixture needs one durable refusal without either visible report: calls=%d reports=%d labels=%v", g.calls, len(effects434Reports(r, 1, message)), r.gh.labelsOf(1))
	}
	effects434Restart(r, hub)
	for i := 0; i < 3; i++ {
		effects434Poll(t, r)
	}
	effects434ExpectReport(t, r, 1, g)
	if g.calls != 1 || len(r.gh.prs) != 0 || len(r.build.built) != 1 {
		t.Fatalf("recovering the report repeated work: calls=%d PRs=%d builds=%d", g.calls, len(r.gh.prs), len(r.build.built))
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	for i := 0; i < 3 && len(r.gh.prs) == 0; i++ {
		effects434Poll(t, r)
	}
	if g.calls != 2 || len(r.gh.prs) != 1 || len(r.build.built) != 1 || len(effects434Reports(r, 1, message)) != 1 {
		t.Fatalf("writer retry did not resume only publication: calls=%d PRs=%d builds=%d reports=%d", g.calls, len(r.gh.prs), len(r.build.built), len(effects434Reports(r, 1, message)))
	}
}

// Consent to retry one refusal cannot also consent to a later refusal the
// writer has not seen. Both the report and that boundary survive restart.
func TestRefusal434EachNewRefusalNeedsAnotherWriterRetry(t *testing.T) {
	r, g := publication434Proposal(t, false)
	const first = "first publication refusal (HTTP 422)"
	const second = "second publication refusal (HTTP 422)"
	g.answer = fmt.Errorf("POST pulls: %s", first)
	publication434Poll(t, r)
	if g.attempts != 1 || publication434Reports(r.gh, first) != 1 {
		t.Fatalf("first refusal: attempts=%d reports=%d", g.attempts, publication434Reports(r.gh, first))
	}
	publication434Restart(t, r)
	g.answer = fmt.Errorf("POST pulls: %s", second)
	r.gh.say(1, "gitdek", "/invariant retry")
	publication434Poll(t, r)
	if g.attempts != 2 || publication434Reports(r.gh, second) != 1 {
		t.Fatalf("second refusal: attempts=%d reports=%d", g.attempts, publication434Reports(r.gh, second))
	}
	publication434Restart(t, r)
	g.answer = nil
	for i := 0; i < 3; i++ {
		publication434Poll(t, r)
	}
	if g.attempts != 2 || len(r.gh.prs) != 0 || !r.gh.issues[1].HasLabel(LabelHumanReview) {
		t.Fatalf("the earlier retry released the later refusal: attempts=%d PRs=%d labels=%v", g.attempts, len(r.gh.prs), r.gh.labelsOf(1))
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	for i := 0; i < 3 && len(r.gh.prs) == 0; i++ {
		publication434Poll(t, r)
	}
	if g.attempts != 3 || len(r.gh.prs) != 1 || len(r.build.built) != 1 || publication434Reports(r.gh, first) != 1 || publication434Reports(r.gh, second) != 1 {
		t.Fatalf("second retry did not resume recorded publication once: attempts=%d PRs=%d builds=%d first=%d second=%d", g.attempts, len(r.gh.prs), len(r.build.built), publication434Reports(r.gh, first), publication434Reports(r.gh, second))
	}
}

type refusedFinalCleanup434GitHub struct {
	GitHub
	calls int
}

const refusedFinalCleanup434Message = "removing the final active label was refused (HTTP 403)"

func (g *refusedFinalCleanup434GitHub) RemoveLabel(ctx context.Context, n int, label string) error {
	if n == 1 && label == LabelPR {
		g.calls++
		if g.calls == 1 {
			return fmt.Errorf("DELETE label: %s", refusedFinalCleanup434Message)
		}
	}
	return g.GitHub.RemoveLabel(ctx, n, label)
}

// A final comment is not permission to discard a refused cleanup. The hold
// wins over terminal cleanup until a retry, and the comment stays unique.
func TestRefusal434FinalCleanupWaitsWithoutRepeatingTheFinalPost(t *testing.T) {
	r, opened := closedPoll434PR(t, false)
	g := &refusedFinalCleanup434GitHub{GitHub: r.f.GitHub}
	r.f.GitHub = g
	pr := r.gh.prs[opened.Marker.PR]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-final-cleanup-434"
	r.gh.issues[1].State = "closed"
	effects434Poll(t, r)
	mergedPosts := func() int {
		n := 0
		for _, p := range r.gh.posts(1) {
			if p.Marker.Kind == KindMerged {
				n++
			}
		}
		return n
	}
	if g.calls != 1 || mergedPosts() != 1 || len(effects434Reports(r, 1, refusedFinalCleanup434Message)) != 1 {
		t.Fatalf("cleanup failure must follow one durable final post: calls=%d merged=%d reports=%d", g.calls, mergedPosts(), len(effects434Reports(r, 1, refusedFinalCleanup434Message)))
	}
	effects434Restart(r, g)
	for i := 0; i < 3; i++ {
		effects434Poll(t, r)
	}
	if g.calls != 1 || mergedPosts() != 1 || !r.gh.issues[1].HasLabel(LabelPR) || !r.gh.issues[1].HasLabel(LabelHumanReview) {
		t.Fatalf("terminal cleanup ignored its refusal: calls=%d merged=%d labels=%v", g.calls, mergedPosts(), r.gh.labelsOf(1))
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	effects434Poll(t, r)
	closedPoll434NoActiveLabels(t, r, 1)
	if g.calls < 2 || mergedPosts() != 1 || len(effects434Reports(r, 1, refusedFinalCleanup434Message)) != 1 || len(r.gh.merged) != 0 {
		t.Fatalf("retry repeated the final result: calls=%d merged=%d reports=%d factory merges=%v", g.calls, mergedPosts(), len(effects434Reports(r, 1, refusedFinalCleanup434Message)), r.gh.merged)
	}
}

type refusedMergedPost434GitHub struct {
	GitHub
	issue      *github.Issue
	mergeCalls int
	postCalls  int
	body       string
}

const refusedMergedPost434Message = "the merged comment was refused (HTTP 422)"

func (g *refusedMergedPost434GitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.mergeCalls++
	merged, err := g.GitHub.Merge(ctx, n, sha, method)
	if err == nil {
		g.issue.State = "closed"
	}
	return merged, err
}

func (g *refusedMergedPost434GitHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); ok && m.Kind == KindMerged {
		g.postCalls++
		if g.postCalls == 1 {
			g.body = body
			return github.Comment{}, fmt.Errorf("POST issue comment: %s", refusedMergedPost434Message)
		}
	}
	return g.GitHub.PostComment(ctx, n, body)
}

// Auto-closing the issue must not replace the refused factory merge post
// with an external-merge observation, or repeat the successful merge work.
func TestRefusal434MergedPostRetainsFactoryAttributionAfterAutoClose(t *testing.T) {
	r := newRig(t)
	opened := ratified(t, r)
	r.gh.ci(opened.Marker.PR, "success")
	builds := len(r.build.built)
	g := &refusedMergedPost434GitHub{GitHub: r.f.GitHub, issue: r.gh.issues[1]}
	r.f.GitHub = g
	effects434Poll(t, r)
	if g.mergeCalls != 1 || g.postCalls != 1 || g.issue.State != "closed" || len(r.gh.deleted) != 1 {
		t.Fatalf("fixture must merge, close, and delete before the post refusal: merges=%d posts=%d issue=%s deleted=%v", g.mergeCalls, g.postCalls, g.issue.State, r.gh.deleted)
	}
	if len(effects434Reports(r, 1, refusedMergedPost434Message)) != 1 || !g.issue.HasLabel(LabelHumanReview) {
		t.Fatal("the refused final post needs its own durable report and hold")
	}
	effects434Restart(r, g)
	for i := 0; i < 3; i++ {
		effects434Poll(t, r)
	}
	if g.postCalls != 1 || g.mergeCalls != 1 {
		t.Fatalf("restart resumed a closed issue's refused post before retry: posts=%d merges=%d", g.postCalls, g.mergeCalls)
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	effects434Poll(t, r)
	var merged []Post
	for _, p := range r.gh.posts(1) {
		if p.Marker.Kind == KindMerged {
			merged = append(merged, p)
		}
	}
	if len(merged) != 1 {
		t.Fatalf("retry must post the final result exactly once: %d posts", len(merged))
	}
	if merged[0].Comment.Body != g.body || !strings.Contains(merged[0].Comment.Body, "I merged it") || merged[0].Marker.Numbers == nil {
		t.Fatalf("retry lost the factory's attribution or original numbers:\n%s", merged[0].Comment.Body)
	}
	effects434Poll(t, r)
	if g.postCalls != 2 || g.mergeCalls != 1 || len(r.gh.merged) != 1 || len(r.gh.deleted) != 1 || len(r.build.built) != builds {
		t.Fatalf("replaying the final post repeated successful work: posts=%d merges=%d deleted=%v builds=%d", g.postCalls, g.mergeCalls, r.gh.deleted, len(r.build.built))
	}
}

type lostMergeRefusalReport434GitHub struct {
	GitHub
	mergeCalls  int
	reportCalls int
}

const lostMergeRefusalReport434Message = "merging is forbidden for this installation (HTTP 403)"

func (g *lostMergeRefusalReport434GitHub) Merge(context.Context, int, string, string) (string, error) {
	g.mergeCalls++
	return "", fmt.Errorf("PUT pull request merge: %s", lostMergeRefusalReport434Message)
}

func (g *lostMergeRefusalReport434GitHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); ok && m.Kind == KindFailed && m.Failure == FailMerge && strings.Contains(body, lostMergeRefusalReport434Message) {
		g.reportCalls++
		if g.reportCalls == 1 {
			return github.Comment{}, fmt.Errorf("POST issue comment: temporarily unavailable (HTTP 503)")
		}
	}
	return g.GitHub.PostComment(ctx, n, body)
}

// The merge refusal already happened even when its report did not reach
// GitHub. A fresh watcher reports it before observing a later issue close.
func TestRefusal434MergeReportRecoveryNeverRepeatsTheMerge(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed=%v", closed), func(t *testing.T) {
			r := newRig(t)
			opened := ratified(t, r)
			r.gh.ci(opened.Marker.PR, "success")
			g := &lostMergeRefusalReport434GitHub{GitHub: r.f.GitHub}
			r.f.GitHub = g
			effects434Poll(t, r)
			if g.mergeCalls != 1 || g.reportCalls != 1 || len(effects434Reports(r, 1, lostMergeRefusalReport434Message)) != 0 {
				t.Fatalf("fixture must lose the report after one refusal: merges=%d report attempts=%d reports=%d", g.mergeCalls, g.reportCalls, len(effects434Reports(r, 1, lostMergeRefusalReport434Message)))
			}
			if closed {
				r.gh.issues[1].State = "closed"
			}
			effects434Restart(r, g)
			effects434Poll(t, r)
			last := r.gh.last(1)
			if last.Marker.Kind != KindFailed || last.Marker.Failure != FailMerge || !strings.Contains(last.Comment.Body, lostMergeRefusalReport434Message) || !strings.Contains(last.Comment.Body, "/invariant retry") {
				t.Fatalf("the original refusal must be reported before any close observation:\n%s", last.Comment.Body)
			}
			for i := 0; i < 3; i++ {
				effects434Poll(t, r)
			}
			if g.mergeCalls != 1 || g.reportCalls != 2 || len(effects434Reports(r, 1, lostMergeRefusalReport434Message)) != 1 || len(r.gh.merged) != 0 {
				t.Fatalf("recovering the refusal repeated the merge or report: merges=%d report attempts=%d reports=%d", g.mergeCalls, g.reportCalls, len(effects434Reports(r, 1, lostMergeRefusalReport434Message)))
			}
		})
	}
}
