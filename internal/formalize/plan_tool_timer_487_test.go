package formalize

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// planTimer487 records the planning job and leaves a draft that runs no
// check, so only the job's own settings are under test.
type planTimer487 struct{ jobs []synth.Job }

func (*planTimer487) Name() string { return "planning timer fixture" }
func (a *planTimer487) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	a.jobs = append(a.jobs, job)
	return synth.Usage{}, os.WriteFile(filepath.Join(job.Workspace, "proposal.json"), []byte(`{"unsupported":"a fixture that drafts nothing"}`+"\n"), 0o644)
}

// #487: a built-in plan's check selects the budget of the packages the plan
// touches, up to internal/verify's hour, and the plan doesn't exist when the
// planning agent starts. Its check tool gets the longest built-in check, with
// the startup and cleanup reserves, rather than the backend's fifteen minutes.
func TestBuiltinPlanningToolCoversTheLongestCheck487(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/fixture\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []time.Duration{40 * time.Minute, 0} {
		agent := &planTimer487{}
		f := Formalizer{Backend: agent, Binary: "unused-fixture-binary", CheckRuns: 1, Timeout: timeout}
		r, err := f.Plan(context.Background(), Request{Issue: 487, Title: "A fixture", Body: "Kind: plumbing\n"}, repo, t.TempDir())
		if err != nil || r == nil {
			t.Fatalf("plan: %+v, %v", r, err)
		}
		if len(agent.jobs) != 1 {
			t.Fatalf("got %d planning jobs, want one", len(agent.jobs))
		}
		want := plumbing.SandboxVerifyTimeout + plumbing.SandboxStartupAllowance + plumbing.SandboxCleanupAllowance
		if got := agent.jobs[0].ToolTimeout; got != want || got != plumbing.PlanCheckToolTimeout() {
			t.Errorf("with a %s run, the planner's check tool gets %s, want %s", timeout, got, want)
		}
	}
}
