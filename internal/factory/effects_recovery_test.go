package factory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// A refusal can have no visible sign: GitHub may reject both its report
// and its human-review label before the watcher restarts.
type hiddenEffectHub struct {
	*effects434GitHub
	hidden bool
}

func (g *hiddenEffectHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); g.hidden && ok && m.Refusal != "" {
		return github.Comment{}, fmt.Errorf("POST issue comment: Service Unavailable (HTTP 503)")
	}
	return g.effects434GitHub.PostComment(ctx, n, body)
}

func (g *hiddenEffectHub) AddLabels(ctx context.Context, n int, labels ...string) error {
	if g.hidden {
		for _, label := range labels {
			if label == LabelHumanReview {
				return fmt.Errorf("POST labels: Resource not accessible by integration (HTTP 403)")
			}
		}
	}
	return g.effects434GitHub.AddLabels(ctx, n, labels...)
}

func TestClosedIssueKeepsAnUnreportedUnlabeledRefusalAcrossWatchers(t *testing.T) {
	r := newRig(t)
	pr := ratified(t, r)
	r.gh.ci(pr.Marker.PR, "success")
	builds, posts := len(r.build.built), len(r.gh.posts(1))
	g := &hiddenEffectHub{effects434GitHub: &effects434GitHub{
		GitHub: r.f.GitHub, op: "delete branch", message: "branch deletion was refused",
		allIssues: r.gh, closeOnMerge: true,
	}, hidden: true}
	r.f.GitHub = g
	effects434Poll(t, r)
	if r.gh.issues[1].State != "closed" || g.calls != 1 || len(r.gh.merged) != 1 || len(r.gh.deleted) != 0 {
		t.Fatal("the fixture must merge and close the issue before branch deletion is refused")
	}
	if len(r.gh.posts(1)) != posts || r.gh.issues[1].HasLabel(LabelHumanReview) {
		t.Fatal("the refusal must have neither a posted report nor a human-review label")
	}
	// No factory label is left to discover this closed issue by.
	r.gh.issues[1].Labels = []github.Label{{Name: "bug"}}
	publication434Restart(t, r)
	for i := 0; i < 2; i++ {
		effects434Poll(t, r)
	}
	if g.calls != 1 || len(r.gh.posts(1)) != posts || len(r.gh.deleted) != 0 {
		t.Fatal("a fresh watcher retried cleanup or finalized the issue before reporting its pending refusal")
	}

	g.hidden = false
	r.poll()
	effects434ExpectReport(t, r, 1, g.effects434GitHub)
	r.gh.say(1, "mallory", "/invariant retry")
	r.poll()
	if g.calls != 1 || len(r.gh.deleted) != 0 {
		t.Fatal("report recovery or a reader's retry repeated the refused deletion")
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	r.expect(1, KindMerged, LabelMerged, "bug")
	if g.calls != 2 || len(r.gh.deleted) != 1 || len(r.gh.merged) != 1 || len(r.build.built) != builds {
		t.Fatalf("writer retry must finish only the remaining cleanup: calls=%d deleted=%v merges=%v builds=%d",
			g.calls, r.gh.deleted, r.gh.merged, len(r.build.built))
	}
	if !r.gh.issues[1].HasLabel("bug") {
		t.Fatal("cleanup removed the unrelated label")
	}
	for _, label := range []string{LabelAsking, LabelProposal, LabelBuilding, LabelPR, LabelHumanReview} {
		if r.gh.issues[1].HasLabel(label) {
			t.Errorf("final cleanup left active label %q", label)
		}
	}
	finalPosts := len(r.gh.posts(1))
	r.poll()
	if len(r.gh.posts(1)) != finalPosts || g.calls != 2 || len(effects434Reports(r, 1, g.message)) != 1 {
		t.Fatal("another poll repeated the refusal, cleanup, or final record")
	}
}

func TestClosedIssueObservationsRequireTheLease(t *testing.T) {
	for _, kind := range []string{KindProposal, KindMerged} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t)
			r.gh.open(1, "gitdek", "Closed work", "", LabelBuilding)
			r.gh.say(1, r.gh.me, post("previous work", "Previous work.", Marker{Kind: kind}))
			r.gh.issues[1].State = "closed"
			r.f.LeaseFor = time.Minute
			// No lease is held. Both a new final observation and cleanup of
			// an existing final record must stop before their first write.
			if err := r.f.Poll(context.Background()); !errors.Is(err, errLeaseLost) {
				t.Fatalf("closed issue without a lease = %v; want the lost-lease error", err)
			}
			if len(r.gh.posts(1)) != 1 || !r.gh.issues[1].HasLabel(LabelBuilding) {
				t.Fatal("the watcher posted or removed a label without its lease")
			}
		})
	}
}

type mergeReportHub struct {
	GitHub
	mergeCalls, reportCalls int
}

func (g *mergeReportHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.mergeCalls++
	if g.mergeCalls == 1 {
		return "", fmt.Errorf("PUT repos/o/r/pulls/%d/merge: merge requires attention (HTTP 422)", n)
	}
	return g.GitHub.Merge(ctx, n, sha, method)
}

func (g *mergeReportHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); ok && m.Kind == KindFailed && m.Failure == FailMerge {
		g.reportCalls++
		if g.reportCalls == 1 {
			return github.Comment{}, fmt.Errorf("POST issue comment: Service Unavailable (HTTP 503)")
		}
	}
	return g.GitHub.PostComment(ctx, n, body)
}

func TestMergeRefusalReportRecoversBeforeAnotherMergeAttempt(t *testing.T) {
	r := newRig(t)
	pr := ratified(t, r)
	r.gh.ci(pr.Marker.PR, "success")
	g := &mergeReportHub{GitHub: r.f.GitHub}
	r.f.GitHub = g
	posts, builds := len(r.gh.posts(1)), len(r.build.built)
	// A retry queued before the refusal is not permission to try it again.
	r.gh.say(1, "gitdek", "/invariant retry")
	if err := r.f.Poll(context.Background()); err == nil {
		t.Fatal("the first refusal report should fail")
	}
	if g.mergeCalls != 1 || g.reportCalls != 1 || len(r.gh.posts(1)) != posts || len(r.gh.merged) != 0 {
		t.Fatal("the refused merge should have one failed report and no successful merge")
	}
	publication434Restart(t, r)
	for i := 0; i < 3; i++ {
		r.poll()
	}
	state, _ := (Thread{Posts: r.gh.posts(1)}).State()
	if state.Marker.Kind != KindFailed || state.Marker.Failure != FailMerge || !r.gh.issues[1].HasLabel(LabelHumanReview) {
		t.Fatalf("recovery must leave the existing merge refusal waiting for a person: %+v", state.Marker)
	}
	if g.mergeCalls != 1 || g.reportCalls != 2 || len(effects434Reports(r, 1, "merge requires attention")) != 1 {
		t.Fatalf("a fresh watcher must finish one report without repeating the merge: merges=%d reports=%d", g.mergeCalls, g.reportCalls)
	}
	r.gh.say(1, "mallory", "/invariant retry")
	r.poll()
	if g.mergeCalls != 1 {
		t.Fatal("a reader's retry repeated the merge")
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	r.expect(1, KindPR, LabelPR)
	r.poll()
	r.expect(1, KindMerged, LabelMerged)
	if g.mergeCalls != 2 || len(r.gh.merged) != 1 || len(r.build.built) != builds {
		t.Fatal("a new writer retry must resume the ordinary merge path without another build")
	}
	r.poll()
	if g.mergeCalls != 2 || g.reportCalls != 2 || len(effects434Reports(r, 1, "merge requires attention")) != 1 {
		t.Fatal("the finished issue repeated its merge or refusal report")
	}
}
