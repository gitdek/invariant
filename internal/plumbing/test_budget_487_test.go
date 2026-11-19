package plumbing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// #487: a plan's check runs only its acceptance tests of the packages the
// plan touches, checks of Invariant's own slow packages get longer budgets,
// a configured part can declare one, and every check says which budget it
// ran under.

// fixture487 is a touched package whose formatting gofmt would change and
// whose other tests fail or outlast the test phase, beside a second touched
// package whose tests don't compile, so go vet fails too. None of it decides
// whether a plan's acceptance test fails on the code as it is.
func fixture487(t *testing.T) (*s397Fixture, string) {
	t.Helper()
	f := s397New(t)
	s397Write(t, filepath.Join(f.root, "slow/code.go"), "package slow\n\nfunc  Report() string { return \"\" }\n", 0o644)
	s397Write(t, filepath.Join(f.root, "slow/code_test.go"), `package slow

import (
	"testing"
	"time"
)

func TestExistingFails(t *testing.T) { t.Fatal("an existing test a plan's check needn't run") }

func TestExistingOutlastsThePhase(t *testing.T) { time.Sleep(time.Minute) }
`, 0o644)
	s397Write(t, filepath.Join(f.root, "other/code.go"), "package other\n", 0o644)
	s397Write(t, filepath.Join(f.root, "other/code_test.go"), "package other\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { undefinedHelper() }\n", 0o644)
	ws := t.TempDir()
	plan, err := json.Marshal(map[string]any{
		"name": "Report the slow package", "summary": "Make Report say something.",
		"files": []string{"slow/code.go", "other/code.go"},
		"tests": []Test{{Name: "TestPinned487", File: "slow/pinned_487_accept_test.go", Says: "Report says something."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(ws, "plan.json"), string(plan), 0o644)
	s397Write(t, filepath.Join(ws, "tests/slow/pinned_487_accept_test.go"),
		"package slow\n\nimport \"testing\"\n\nfunc TestPinned487(t *testing.T) {\n\tif Report() == \"\" {\n\t\tt.Fatal(\"Report says nothing yet\")\n\t}\n}\n", 0o644)
	return f, ws
}

func TestPlanCheckRunsOnlyItsAcceptanceTests487(t *testing.T) {
	f, ws := fixture487(t)
	s := Sandbox{Image: "local-fixture", ModCache: f.mod, Cache: f.cache, Timeout: 40 * time.Second, ShardTimeout: 3 * time.Second}
	started := time.Now()
	c, err := Check(context.Background(), ws, f.root, s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Problem != "" {
		t.Fatalf("the plan's check failed on what it doesn't decide: %s\n%s", c.Problem, c.Run.Output)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Errorf("the plan's check took %s", elapsed)
	}
	// gofmt and vet still run, as before; their findings decide nothing.
	if len(c.Run.Gofmt) != 1 || c.Run.Gofmt[0] != "slow/code.go" || c.Run.Vet {
		t.Errorf("the plan's check skipped gofmt or vet: gofmt=%v vet=%t", c.Run.Gofmt, c.Run.Vet)
	}
	runs := map[string]string{}
	for _, r := range c.Run.TestResults {
		if r.Name != "TestPinned487" {
			t.Errorf("the plan's check ran %s, which it doesn't pin, in its %s phase", r.Name, r.Phase)
		}
		runs[r.Phase] = r.Status
	}
	if runs["test"] != "failed" || runs["accept"] != "failed" {
		t.Fatalf("the acceptance test's runs: %v, want a completed failure in the test phase and in its replay", runs)
	}
	for _, problem := range c.Run.Problems {
		if strings.Contains(problem, "build") && strings.Contains(problem, "other") {
			t.Errorf("the plan's check built a package that holds none of its acceptance tests: %s", problem)
		}
	}
	if !c.Run.AcceptanceOnly || c.Run.RunBudget != 40*time.Second || c.Run.TestBudget != 3*time.Second {
		t.Errorf("the check's record lost what it ran under: only=%t run=%s tests=%s", c.Run.AcceptanceOnly, c.Run.RunBudget, c.Run.TestBudget)
	}
	summary := c.Run.Summary()
	for _, want := range []string{"The checks ran under a test budget of 3s, inside a whole run of 40s.", "Of the packages the plan touches, only its acceptance tests ran, as a plan's check runs them."} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary doesn't say %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "ran out") {
		t.Errorf("the summary says a budget ran out:\n%s", summary)
	}
}

// A build's checks still run everything: the same package, with the change
// made, gets gofmt, vet and every test of the packages it touches, and the
// test phase runs out of its short budget here.
func TestBuildChecksStillRunEveryPhase487(t *testing.T) {
	f, ws := fixture487(t)
	data, err := os.ReadFile(filepath.Join(ws, "tests/slow/pinned_487_accept_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(f.root, "slow/pinned_487_accept_test.go"), string(data), 0o644)
	plan, err := ReadDraft(ws)
	if err != nil {
		t.Fatal(err)
	}
	r, err := (Sandbox{Image: "local-fixture", ModCache: f.mod, Cache: f.cache, Timeout: 40 * time.Second, ShardTimeout: 3 * time.Second}).Run(context.Background(), f.root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if r.AcceptanceOnly || len(r.Gofmt) == 0 || r.Vet || !r.TimedOut {
		t.Fatalf("a build's check skipped a phase: only=%t gofmt=%v vet=%t timedout=%t\n%s", r.AcceptanceOnly, r.Gofmt, r.Vet, r.TimedOut, r.Output)
	}
	ran := map[string]bool{}
	for _, result := range r.TestResults {
		if result.Phase == "test" {
			ran[result.Name] = true
		}
	}
	if !ran["TestExistingFails"] || !ran["TestExistingOutlastsThePhase"] || !ran["TestPinned487"] {
		t.Errorf("a build's check ran %v in its test phase", ran)
	}
	if summary := r.Summary(); !strings.Contains(summary, "The checks ran under a test budget of 3s, inside a whole run of 40s.") ||
		!strings.Contains(summary, "The test budget of 3s ran out before the tests finished.") || strings.Contains(summary, "only its acceptance tests") {
		t.Errorf("a check the budget cut off doesn't say so:\n%s", summary)
	}
}

func TestBuiltinChecksSelectThePackagesBudget487(t *testing.T) {
	record := v498Spy(t)
	root := t.TempDir()
	for _, dir := range []string{"internal/factory", "internal/factory/sub", "internal/verify", "internal/plumbing", "internal/dashboard", "internal/formalize"} {
		s397Write(t, filepath.Join(root, dir, "code.go"), "package fixture\n", 0o644)
	}
	s397Write(t, filepath.Join(root, "go.mod"), "module github.com/gitdek/invariant\n\ngo 1.27.1\n", 0o644)
	ctx := v498Watchdog(t)
	for _, tc := range []struct {
		name          string
		files         []string
		overall       time.Duration
		phase         time.Duration
		run, tests    time.Duration
		summaryBudget string
	}{
		{name: "factory", files: []string{"internal/factory/code.go"}, run: 30 * time.Minute, tests: 25 * time.Minute, summaryBudget: "a test budget of 25 minutes, inside a whole run of 30 minutes"},
		{name: "947s_packages", files: []string{"internal/formalize/code.go", "internal/factory/code.go", "internal/dashboard/code.go"}, run: 30 * time.Minute, tests: 25 * time.Minute},
		{name: "factory_and_verify", files: []string{"internal/factory/code.go", "internal/verify/code.go"}, run: 60 * time.Minute, tests: 55 * time.Minute},
		{name: "child_package", files: []string{"internal/factory/sub/code.go"}, run: 12 * time.Minute, tests: 10 * time.Minute},
		{name: "ordinary", files: []string{"internal/dashboard/code.go"}, run: 12 * time.Minute, tests: 10 * time.Minute, summaryBudget: "a test budget of 10 minutes, inside a whole run of 12 minutes"},
		{name: "plumbing", files: []string{"internal/plumbing/code.go"}, run: 30 * time.Minute, tests: 25 * time.Minute},
		{name: "plumbing_and_verify", files: []string{"internal/plumbing/code.go", "internal/verify/code.go"}, run: 60 * time.Minute, tests: 55 * time.Minute},
		{name: "shorter_overrides", files: []string{"internal/factory/code.go"}, overall: 20 * time.Minute, phase: 15 * time.Minute, run: 20 * time.Minute, tests: 15 * time.Minute},
		{name: "cannot_extend", files: []string{"internal/factory/code.go"}, overall: 2 * time.Hour, phase: 2 * time.Hour, run: 30 * time.Minute, tests: 25 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Sandbox{Image: "fixture", ModCache: t.TempDir(), Timeout: tc.overall, ShardTimeout: tc.phase}
			started := time.Now()
			r, err := s.Run(ctx, root, &Plan{Files: tc.files})
			if err != nil || !r.Passed() {
				t.Fatalf("configuration fixture: %+v, %v", r, err)
			}
			all := v498Read(t, record)
			v498Budgets(t, all[len(all)-1], started, tc.run, tc.tests)
			if r.RunBudget != tc.run || r.TestBudget != tc.tests {
				t.Errorf("the result says it ran under %s inside %s, want %s inside %s", r.TestBudget, r.RunBudget, tc.tests, tc.run)
			}
			if tc.summaryBudget != "" && !strings.Contains(r.Summary(), tc.summaryBudget) {
				t.Errorf("the summary doesn't say %q:\n%s", tc.summaryBudget, r.Summary())
			}
		})
	}
}

// A direct build's tool timer covers a factory check, and a planning
// agent's covers the longest built-in check, whatever its plan touches.
func TestToolTimersCoverTheLongerBudgets487(t *testing.T) {
	p := &Plan{Files: []string{"internal/factory/code.go"}, Tests: []Test{{Name: "TestFixture", File: "internal/factory/fixture_accept_test.go"}}}
	if got := buildSandboxTimeout(p, []string{"internal/factory/code.go", "go.mod"}); got != 30*time.Minute {
		t.Errorf("a direct build of internal/factory sizes its tool for %s, want 30m", got)
	}
	if got := buildSandboxTimeout(&Plan{Files: []string{"internal/scope/code.go"}}, []string{"internal/scope/code.go"}); got != SandboxTimeout {
		t.Errorf("a direct build of an ordinary package sizes its tool for %s, want %s", got, SandboxTimeout)
	}
	// A plan can delete a child's last Go file, leaving factory to own it.
	child := &Plan{Files: []string{"internal/factory/sub/code.go"}}
	if got := buildSandboxTimeout(child, []string{"internal/factory/sub/code.go", "internal/factory/code.go"}); got != 30*time.Minute {
		t.Errorf("a build that can leave internal/factory owning its paths sizes its tool for %s, want 30m", got)
	}
	if got, want := PlanCheckToolTimeout(), SandboxVerifyTimeout+SandboxStartupAllowance+SandboxCleanupAllowance; got != want || got != 61*time.Minute+15*time.Second {
		t.Errorf("a planning agent's check tool gets %s, want %s", got, want)
	}
	for dir, b := range builtinBudgets {
		if PlanCheckToolTimeout() < b.run+SandboxStartupAllowance+SandboxCleanupAllowance {
			t.Errorf("a plan check of %s can outlast the planning tool's timer", dir)
		}
	}
}

// The most a part can declare is the longest built-in budget, and the
// default is the ordinary one.
func TestDeclaredBudgetsMatchTheSandbox487(t *testing.T) {
	if time.Duration(repository.MaxTestMinutes)*time.Minute != SandboxVerifyShardTimeout {
		t.Errorf("a part can declare %d minutes, but the longest built-in test budget is %s", repository.MaxTestMinutes, SandboxVerifyShardTimeout)
	}
	if time.Duration(repository.DefaultTestMinutes)*time.Minute != SandboxShardTimeout {
		t.Errorf("a part's default is %d minutes, but the ordinary test budget is %s", repository.DefaultTestMinutes, SandboxShardTimeout)
	}
}

// A configured part's declared budget is the test phase of every check that
// selects it, and the whole run grows with it. A part that declares none
// keeps the default, even beside one that does.
func TestConfiguredPartsDeclareTheirTestBudget487(t *testing.T) {
	parts := []repository.Part{part484("go", "api"), part484("typescript", "web")}
	parts[0].TestMinutes = 30
	root, candidate, snapshot := fixture484(t, parts, nil)
	for _, tc := range []struct {
		name       string
		files      []string
		run, tests time.Duration
	}{
		{"declared", []string{"api/main.go"}, 32 * time.Minute, 30 * time.Minute},
		{"undeclared", []string{"web/main.ts"}, 12 * time.Minute, 10 * time.Minute},
		{"both", []string{"api/main.go", "web/main.ts"}, 32 * time.Minute, 30 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunner484(t)
			s := Sandbox{Original: root, Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}}}
			started := time.Now()
			result, err := s.Run(context.Background(), candidate, &Plan{Files: tc.files})
			if err != nil || !result.Passed() {
				t.Fatalf("declared checks: %v\n%s", err, result.Summary())
			}
			if result.RunBudget != tc.run || result.TestBudget != tc.tests {
				t.Errorf("the result ran under %s inside %s, want %s inside %s", result.TestBudget, result.RunBudget, tc.tests, tc.run)
			}
			tested := false
			for _, call := range r.calls {
				left := call.deadline.Sub(started)
				// The test phase starts at the first test command and ends
				// its budget after that, within the whole run. What any part
				// runs later shares it, as before.
				tested = tested || call.argv[1] == "test"
				if !tested {
					if left < tc.run-2*time.Second || left > tc.run+time.Second {
						t.Errorf("%s/%s had %s of the whole run, want %s", call.request.Directory, call.argv[1], left, tc.run)
					}
				} else if left < tc.tests || left > tc.tests+5*time.Second {
					t.Errorf("%s/%s had %s, want the %s test budget", call.request.Directory, call.argv[1], left, tc.tests)
				}
			}
			if !tested {
				t.Fatal("no test command ran")
			}
			want := "The checks ran under a test budget of " + budgetWords(tc.tests) + ", inside a whole run of " + budgetWords(tc.run) + "."
			if !strings.Contains(result.Summary(), want) {
				t.Errorf("the summary doesn't say %q:\n%s", want, result.Summary())
			}
		})
	}
	t.Run("cut_off", func(t *testing.T) {
		r := newRunner484(t)
		r.hook = func(ctx context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
			if argv[1] == "test" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return []byte("ok\n"), nil
		}
		// A shorter override still applies to a declared budget.
		s := Sandbox{Original: root, ShardTimeout: time.Second, Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}}}
		result, err := s.Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go"}})
		if result.Passed() || !result.TimedOut || result.TestBudget != time.Second || result.RunBudget != 32*time.Minute {
			t.Fatalf("a budget that ran out: %+v, %v", result, err)
		}
		if summary := result.Summary(); !strings.Contains(summary, "The test budget of 1s ran out before the tests finished.") {
			t.Errorf("a check the budget cut off doesn't say so:\n%s", summary)
		}
	})
}

// check-repository bounds a configured check from outside, from its start.
// That bound grows with the longest budget the contract declares.
func TestRepositoryRunBudgetGrowsWithTheLongestDeclaration487(t *testing.T) {
	parts := []repository.Part{part484("go", "api"), part484("typescript", "web")}
	_, _, plain := fixture484(t, parts, nil)
	if got := RepositoryRunBudget(plain); got != SandboxTimeout {
		t.Errorf("a contract that declares no budget bounds checks at %s, want %s", got, SandboxTimeout)
	}
	parts[1].TestMinutes = 40
	_, _, declared := fixture484(t, parts, nil)
	if got := RepositoryRunBudget(declared); got != 42*time.Minute {
		t.Errorf("a contract with a 40-minute part bounds checks at %s, want 42m", got)
	}
	if got := RepositoryRunBudget(nil); got != SandboxTimeout {
		t.Errorf("no contract bounds checks at %s", got)
	}
}
