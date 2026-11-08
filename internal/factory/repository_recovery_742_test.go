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

type holdWrites742 struct {
	GitHub
	failReport bool
	writes     int
}

func (g *holdWrites742) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	g.writes++
	if g.failReport && strings.Contains(body, "repository authority needs a person") {
		g.failReport = false
		return github.Comment{}, errors.New("connection reset while reporting the hold")
	}
	return g.GitHub.PostComment(ctx, n, body)
}

func (g *holdWrites742) AddLabels(ctx context.Context, n int, labels ...string) error {
	g.writes++
	return g.GitHub.AddLabels(ctx, n, labels...)
}

func (g *holdWrites742) RemoveLabel(ctx context.Context, n int, label string) error {
	g.writes++
	return g.GitHub.RemoveLabel(ctx, n, label)
}

func savedPublication742(t *testing.T, x *trust695, file string) Post {
	t.Helper()
	_, code, _, ratified := x.candidate(t, []string{file}, map[string]string{file: "changed\n"}, nil)
	built := builtResult{Plumbing: &plumbing.BuildResult{Passed: true, Approved: true, Tests: 1, Summary: "Checked and reviewed."}}
	result, err := x.r.f.saveResult(t.Context(), built, "finished build", code)
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
	return ratified
}

func restartHold742(x *trust695, gh GitHub, repo Repo) {
	old := x.r.f
	x.r.f = &Factory{Repository: old.Repository, GitHub: gh, Repo: repo, Formalizer: old.Formalizer,
		Builder: old.Builder, Plumbing: old.Plumbing, Base: old.Base, Projects: old.Projects,
		Work: old.Work, Check: old.Check, Log: old.Log}
}

func TestRepositoryHoldRecoversAnInterruptedReport742(t *testing.T) {
	x := newTrust695(t)
	savedPublication742(t, x, "app/main.go")
	probe := &publication742{Repo: x.c}
	writes := &holdWrites742{GitHub: x.g, failReport: true}
	x.r.f.Repo, x.r.f.GitHub = probe, writes
	x.advance(t, func(*repository.Config) {}, true)
	if err := x.r.f.Poll(t.Context()); err == nil {
		t.Fatal("the interrupted hold report must be retried")
	}
	loads := probe.loads
	restartHold742(x, writes, probe)
	x.r.poll()
	if probe.loads != loads || !strings.Contains(x.r.gh.last(1).Comment.Body, repository.Path) {
		t.Fatal("recovering a hold reloaded the build or lost its specific reason")
	}
	before := writes.writes
	x.r.poll()
	x.r.poll()
	x.r.gh.say(1, "mallory", "/invariant retry")
	x.r.poll()
	if writes.writes != before || probe.loads != loads {
		t.Fatal("an idle hold or reader retry repeated publication or reporting writes")
	}
	x.advance(t, func(*repository.Config) {}, false)
	restartHold742(x, writes, probe)
	x.r.poll()
	if writes.writes != before || probe.loads != loads {
		t.Fatal("restoring authority released the hold without a writer")
	}
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	x.r.expect(1, KindPR, LabelPR)
	if probe.loads <= loads || len(x.g.prs) != 1 || len(x.r.build.built) != 0 {
		t.Fatal("retry must resume the recorded publication")
	}
}

func TestWorkflowCredentialHoldMakesNoRepeatedLabelWrites742(t *testing.T) {
	x := newTrust695(t)
	savedPublication742(t, x, ".github/workflows/invariant.yml")
	denied := &workflowRetryRepo{Repo: x.c}
	writes := &holdWrites742{GitHub: x.g}
	x.r.f.Repo, x.r.f.GitHub = denied, writes
	_ = x.r.f.Poll(t.Context())
	if denied.pushes != 1 {
		t.Fatal("workflow push did not reach its credential refusal")
	}
	before := writes.writes
	restartHold742(x, writes, denied)
	x.r.poll()
	x.r.poll()
	x.r.gh.say(1, "mallory", "/invariant retry")
	x.r.poll()
	if writes.writes != before || denied.pushes != 1 {
		t.Fatal("the visible credential hold repeated a write without a writer")
	}
	denied.allow = true
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	if denied.pushes != 2 || len(x.g.prs) != 1 || len(x.g.calls) != 0 {
		t.Fatal("authorized retry did not publish the preserved trusted draft")
	}
}

func TestNativeLockCannotReplaceCapturedBase742(t *testing.T) {
	x := newTrust695(t)
	p, _, _, ratified := x.candidate(t, []string{"app/main.go"}, map[string]string{"app/main.go": "changed\n"}, nil)
	x.advance(t, func(*repository.Config) {}, false)
	if err := x.c.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := x.c.RevParse(t.Context(), "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	changed, execution := *p, *p.Execution
	execution.Base, changed.Execution = current, &execution
	if changed.Hash() != p.Hash() {
		t.Fatal("base provenance must remain outside the legacy native hash")
	}
	if _, err := x.r.f.lockedRepository(t.Context(), ratified.Marker, &changed); err == nil {
		t.Fatal("candidate substituted execution provenance outside the hash")
	}
}

func TestOutOfPlanCommitIsRefusedBeforePublication742(t *testing.T) {
	x := newTrust695(t)
	_, code, _, ratified := x.candidate(t, []string{"app/main.go"}, map[string]string{
		"app/main.go": "package app\nconst Value = 2\n", "unplanned.txt": "outside the plan\n",
	}, nil)
	built := builtResult{Plumbing: &plumbing.BuildResult{Passed: true, Approved: true}}
	result, err := x.r.f.saveResult(t.Context(), built, "finished out-of-plan candidate", code)
	if err != nil {
		t.Fatal(err)
	}
	probe := &publication742{Repo: x.c}
	x.r.f.Repo = probe
	next := ratified.Marker.carried()
	err = x.r.f.publishPlan(t.Context(), x.thread(t), ratified, result, built, next, 0)
	if err == nil || probe.pushes != 0 || len(x.g.prs) != 0 {
		t.Fatalf("out-of-plan candidate reached publication: %v, pushes=%d PRs=%d", err, probe.pushes, len(x.g.prs))
	}
}
