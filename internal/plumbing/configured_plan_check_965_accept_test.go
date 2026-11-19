package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// Exercise the public configured path with real local Go processes. The
// established fixture substitutes only dependency-image preparation.
func TestConfiguredGoPlanCheckOnlyPinned965(t *testing.T) {
	source := repositorySource582(`
		t.Run("selected.[x]+", func(t *testing.T) {
			if Value < 7 { t.Fatal("pinned selection needs at least seven") }
			t.Log("pinned selection completed")
		})
		t.Run("neighbor", func(t *testing.T) { fmt.Println("UNPINNED_CHILD_965") })
	`, "")
	f := newRepositoryFixture582(t, t.TempDir(), []repositoryCase582{
		{"api", "nested", "TestReady/selected.[x]+", source},
	}, true, map[string]string{
		"api/nested/ordinary_test.go": `package fixture
import ("fmt"; "testing")
func TestOrdinary965(t *testing.T) {
    fmt.Println("ORDINARY_SUITE_965")
    if Value != 7 { t.Fatal("ordinary suite needs exactly seven") }
}
`,
	}, nil)
	f.sandbox.Repository.Adapters = nil
	checked, err := Check(t.Context(), n503Draft(t, f.plan), f.root, f.sandbox)
	if err != nil || checked == nil || checked.Problem != "" {
		t.Fatalf("configured proposal did not establish its pinned failure: %v %+v", err, checked)
	}
	assertPlanningSummary965(t, checked, "api")
	repositoryResult582(t, checked.Run, f.plan.Tests[0], "failed", "pinned selection needs at least seven")
	for _, marker := range []string{"UNPINNED_CHILD_965", "ORDINARY_SUITE_965"} {
		if strings.Contains(checked.Run.Output, marker) {
			t.Errorf("proposal ran an unpinned test: %s", marker)
		}
	}
	for _, observation := range f.runner.observed {
		if observation.stage == "suite" || slices.Equal(observation.argv, []string{"go", "build", "./..."}) {
			t.Errorf("proposal ran a broad command: %q", observation.argv)
		}
	}
	for _, phase := range []string{"format", "lint"} {
		if status484(t, checked.Run, "api", phase) != "passed" {
			t.Errorf("proposal omitted %s", phase)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, f.plan.Tests[0].File)); !os.IsNotExist(err) {
		t.Fatalf("proposal changed the original checkout: %v", err)
	}

	// A production correction must still pass all ordinary checks and then
	// supply a fresh runtime pass for the selected acceptance test.
	f.candidateValue(7)
	start := len(f.runner.observed)
	result, err := f.sandbox.Run(t.Context(), f.candidate, checked.Plan)
	if err != nil || !result.Passed() {
		t.Fatalf("corrected build failed: %v\n%s", err, result.Summary())
	}
	for _, phase := range []string{"format", "lint", "build", "test"} {
		if status484(t, result, "api", phase) != "passed" {
			t.Errorf("build omitted %s", phase)
		}
	}
	built, tested := false, false
	for _, observation := range f.runner.observed[start:] {
		built = built || slices.Equal(observation.argv, []string{"go", "build", "./..."})
		tested = tested || observation.stage == "suite"
	}
	if !built || !tested {
		t.Fatal("build reported success without running its broad build and suite")
	}
	repositoryResult582(t, result, f.plan.Tests[0], "passed", "pinned selection completed")

	// Eight still satisfies the pinned test, but breaks an existing test.
	// Narrowing planning must not allow that candidate through build checks.
	f.candidateValue(8)
	result, err = f.sandbox.Run(t.Context(), f.candidate, checked.Plan)
	if err != nil || result.Passed() || status484(t, result, "api", "test") != "failed" || !strings.Contains(result.Output, "ordinary suite needs exactly seven") {
		t.Fatalf("build bypassed an ordinary regression: %v\n%s\n%s", err, result.Summary(), result.Output)
	}
}

// The existing trusted environment fixture checks literal selector arguments,
// fresh input copies and cleanup for every framework; real Go is covered above.
func TestConfiguredPlanCheckDirectAndQueued965(t *testing.T) {
	for _, framework := range []string{"go", "pytest", "vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			sandbox, _, plan, runner := fixtureIsolation621(t, framework, true)
			runner.base = true
			workspace := n503Draft(t, plan)
			queue, err := NewCheckQueue(1)
			if err != nil {
				t.Fatal(err)
			}
			defer queue.Close()
			client, finish, err := queue.RegisterPlan(t.Context(), sandbox, sandbox.Original)
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			for _, mode := range []string{"queued planner tool", "fresh final check"} {
				startCommands, startTests := len(runner.calls), len(runner.accepted)
				checker := sandbox
				if mode == "queued planner tool" {
					checker = Sandbox{Checks: client}
				}
				checked, err := Check(t.Context(), workspace, sandbox.Original, checker)
				if err != nil || checked == nil || checked.Problem != "" {
					t.Fatalf("%s: %v %+v", mode, err, checked)
				}
				assertPlanningSummary965(t, checked, "app")
				if checked.Plan.Hash() != plan.Hash() || len(runner.accepted)-startTests != len(plan.Tests) {
					t.Fatalf("%s changed authority or did not run each selector once", mode)
				}
				commands := runner.calls[startCommands:]
				part := sandbox.Repository.Snapshot.Config().Parts[0]
				if len(commands) != 2 {
					t.Fatalf("%s ran %d commands; only format and lint belong before the pinned tests", mode, len(commands))
				}
				for i, phase := range []string{"format", "lint"} {
					if !slices.Equal(commands[i].argv, part.Commands[phase].Argv) || status484(t, checked.Run, "app", phase) != "passed" {
						t.Fatalf("%s lost the captured %s command: %q", mode, phase, commands[i].argv)
					}
				}
			}
			// A narrower check still needs fresh failures, not merely execution.
			runner.base = false
			checked, err := Check(t.Context(), workspace, sandbox.Original, Sandbox{Checks: client})
			if err != nil || checked == nil || checked.Problem == "" {
				t.Fatalf("already-passing selectors qualified: %v %+v", err, checked)
			}
			for _, test := range plan.Tests {
				if result515(t, checked.Run, test).Status != "passed" || !strings.Contains(checked.Feedback(), test.Identity()) {
					t.Fatalf("refusal lost the passing selector %s", test.Identity())
				}
			}
			closed484(t, runner.runner484)
		})
	}
}

func TestConfiguredPlanCheckAcceptanceBudget965(t *testing.T) {
	f := newFixture515(t, "api", "web")
	// web is touched by the plan but holds no pinned acceptance selector.
	f.plan.Tests = f.plan.Tests[:2]
	delete(f.plan.Sources, "web/lifecycle_accept_test.go")
	recapture515(t, f, func(config *repository.Config) {
		for i := range config.Parts {
			for _, phase := range []string{"format", "lint"} {
				config.Parts[i].Commands[phase] = repository.Command{NotApplicable: "No " + phase + " check for this fixture."}
			}
		}
	})
	f.sandbox.Timeout = 30 * time.Second
	f.sandbox.ShardTimeout = 5 * time.Second
	allFail515(f.runner)
	var deadlines []time.Time
	f.runner.openHook = func(ctx context.Context, request DependencyRequest) error {
		end, ok := ctx.Deadline()
		if !ok || end.After(time.Now().Add(f.sandbox.ShardTimeout)) {
			t.Error("acceptance startup did not inherit the shared test allowance")
		}
		if request.Directory != "api" {
			t.Error("a touched part without acceptance or format/lint work opened a command environment")
		}
		deadlines = append(deadlines, end)
		return nil
	}
	checked, err := Check(t.Context(), n503Draft(t, f.plan), f.root, f.sandbox)
	if err != nil || checked == nil || checked.Problem != "" {
		t.Fatalf("acceptance-only proposal failed: %v %+v", err, checked)
	}
	assertPlanningSummary965(t, checked, "api", "web")
	if len(f.runner.calls) != 0 || len(deadlines) != len(f.plan.Tests) || len(f.runner.invocations) != len(f.plan.Tests) {
		t.Fatalf("unexpected commands or environments: commands=%d startups=%d selectors=%d", len(f.runner.calls), len(deadlines), len(f.runner.invocations))
	}
	for _, end := range deadlines[1:] {
		if !end.Equal(deadlines[0]) {
			t.Error("a later acceptance invocation renewed the shared test allowance")
		}
	}
	for _, part := range []string{"api", "web"} {
		for _, phase := range []string{"format", "lint"} {
			if status484(t, checked.Run, part, phase) != "skipped" || !strings.Contains(checked.Run.Summary(), "No "+phase+" check for this fixture.") {
				t.Errorf("lost captured inapplicability for %s/%s", part, phase)
			}
		}
	}
	closed484(t, f.runner.runner484)
}

func assertPlanningSummary965(t *testing.T, checked *Checked, parts ...string) {
	t.Helper()
	// The result must remain informative after raw diagnostic output is lost.
	result := checked.Run
	result.Output = ""
	summary := result.Summary()
	for _, part := range parts {
		for _, phase := range []string{"build", "test"} {
			if status484(t, result, part, phase) != "skipped" {
				t.Errorf("proposal must skip %s/%s: %s", part, phase, summary)
			}
			for _, command := range result.CommandResults {
				if command.Part == part && command.Phase == phase && (command.Reason == "" || !strings.Contains(summary, command.Reason)) {
					t.Errorf("summary must explain the omitted %s/%s phase", part, phase)
				}
			}
			if !strings.Contains(summary, part+"/"+phase+": skipped") {
				t.Errorf("summary misstates %s/%s: %s", part, phase, summary)
			}
		}
	}
	if len(result.TestResults) != len(checked.Plan.Tests) {
		t.Fatalf("proposal must report only its pinned selections: %+v", result.TestResults)
	}
	feedback := *checked
	feedback.Run = result
	for _, test := range checked.Plan.Tests {
		record := result515(t, result, test)
		if record.Status != "failed" || record.Kind != "test" {
			t.Errorf("selector lacks its own completed assertion failure: %+v", record)
		}
		if !strings.Contains(summary, test.Identity()) || !strings.Contains(feedback.Feedback(), test.Identity()) {
			t.Errorf("summary or planner feedback lost %s", test.Identity())
		}
	}
}
