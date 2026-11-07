package factory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

type moveAfterReady struct {
	*draftGitHub
	move func()
}

func (g *moveAfterReady) MarkReady(ctx context.Context, n int) error {
	if err := g.draftGitHub.MarkReady(ctx, n); err != nil {
		return err
	}
	g.move()
	return nil
}

func TestConfiguredMergeRefreshesAfterReadiness(t *testing.T) {
	x := newTrust695(t)
	n, _ := x.watched(t, "app/main.go", nil)
	x.r.f.GitHub = &moveAfterReady{x.g, func() {
		x.advance(t, func(c *repository.Config) { c.TrustedPaths = []string{"policy/", "app/main.go"} }, false)
	}}
	x.r.poll()
	x.r.expect(1, KindFailed, LabelHumanReview)
	if x.g.prs[n].Draft || len(x.g.calls) != 1 || !strings.HasPrefix(x.g.calls[0], "ready #") || len(x.g.merged) != 0 {
		t.Fatalf("base changed during readiness but reached merge: calls=%v, PR=%+v", x.g.calls, x.g.prs[n])
	}
}

type workflowRetryRepo struct {
	Repo
	allow  bool
	pushes int
}

func (r *workflowRetryRepo) PushCommit(ctx context.Context, code, branch string) error {
	r.pushes++
	if !r.allow {
		return errors.New("remote: refusing to allow a Personal Access Token to create or update workflow `.github/workflows/invariant.yml` without `workflow` scope")
	}
	return r.Repo.PushCommit(ctx, code, branch)
}

func TestWorkflowPermissionRetryPublishesThePreservedBuild(t *testing.T) {
	x := newTrust695(t)
	_, code, _, ratified := x.candidate(t, []string{".github/workflows/invariant.yml"}, map[string]string{".github/workflows/invariant.yml": "name: changed\n"}, nil)
	built := builtResult{Plumbing: &plumbing.BuildResult{Passed: true, Approved: true, Tests: 1, Summary: "Checks passed.", Review: "VERDICT: approve"}}
	result, err := x.r.f.saveResult(t.Context(), built, "preserved build", code)
	if err != nil {
		t.Fatal(err)
	}
	step := buildStepName(ratified)
	if err := x.c.Record(t.Context(), 1, step, "build started"); err != nil {
		t.Fatal(err)
	}
	if err := x.c.Finish(t.Context(), 1, step, result); err != nil {
		t.Fatal(err)
	}
	r := &workflowRetryRepo{Repo: x.c}
	x.r.f.Repo = r
	_ = x.r.f.Poll(t.Context())
	if r.pushes != 1 || len(x.g.prs) != 0 {
		t.Fatalf("refused push: pushes=%d, PRs=%d", r.pushes, len(x.g.prs))
	}
	r.allow = true
	x.r.gh.say(1, "outsider", "/invariant retry")
	x.r.poll()
	if r.pushes != 1 {
		t.Fatal("credential repair or an outsider's command released the hold")
	}
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	if r.pushes != 2 || len(x.g.prs) != 1 || len(x.g.calls) != 0 || len(x.g.merged) != 0 {
		t.Fatalf("retry lost the recorded publication: pushes=%d, PRs=%d, calls=%v", r.pushes, len(x.g.prs), x.g.calls)
	}
	for _, pr := range x.g.prs {
		if !pr.Draft {
			t.Fatal("workflow retry readied trusted work")
		}
	}
}

func TestWorkflowPermissionClassification(t *testing.T) {
	for _, message := range []string{
		"refusing to allow a GitHub App to create or update workflow without workflows permission",
		"refusing to allow an OAuth App to create or update workflow without workflow scope",
	} {
		if !permanentEffect(errors.New(message)) {
			t.Errorf("workflow refusal not durable: %s", message)
		}
	}
	for _, message := range []string{"failed to push some refs", "workflow connection reset", "workflow permission check timed out"} {
		if permanentEffect(errors.New(message)) {
			t.Errorf("ordinary push failure became a permission handoff: %s", message)
		}
	}
}

func TestConfiguredPublicationRefreshesTheBase(t *testing.T) {
	x := newTrust695(t)
	_, code, branch, ratified := x.candidate(t, []string{"app/main.go"}, map[string]string{"app/main.go": "package app\nconst Value = 2\n"}, nil)
	if err := x.c.PushCommit(t.Context(), code, branch); err != nil {
		t.Fatal(err)
	}
	if _, err := x.g.CreatePullRequest(t.Context(), github.NewPullRequest{Head: branch, Base: "main", Draft: true}); err != nil {
		t.Fatal(err)
	}
	built := builtResult{Plumbing: &plumbing.BuildResult{Passed: true, Approved: true}}
	result, err := x.r.f.saveResult(t.Context(), built, "build result", code)
	if err != nil {
		t.Fatal(err)
	}
	x.r.f.Repo = &moveBaseTrust695{Repo: x.c, move: func() {
		x.advance(t, func(c *repository.Config) { c.TrustedPaths = []string{"policy/", "app/main.go"} }, false)
	}}
	next := ratified.Marker.carried()
	next.Kind = KindFailed
	err = x.r.f.publishPlan(t.Context(), x.thread(t), ratified, result, built, next, 0)
	if err == nil || !strings.Contains(err.Error(), "protocol") || len(x.g.calls) != 0 {
		t.Fatalf("publication ignored the changed base: %v, calls=%v", err, x.g.calls)
	}
}
