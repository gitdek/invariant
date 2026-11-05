package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

// Real local Go supplies all runtime/compiler outcomes. Only image preparation
// uses #582's stand-in. In particular, no adapter is explicitly registered.
func configured610(t *testing.T, cache string, cases []repositoryCase582, commands bool) *repositoryFixture582 {
	t.Helper()
	f := newRepositoryFixture582(t, cache, cases, commands, nil, nil)
	f.sandbox.Repository.Adapters = nil
	return f
}

func check610(t *testing.T, f *repositoryFixture582) (*Checked, error) {
	t.Helper()
	ws := n503Draft(t, f.plan)
	// The agent's copy is not the independent base used by Check.
	write468(t, ws, "repo/api/nested/value.go", "package fixture\nconst Value=7\n")
	write468(t, ws, "repo/"+repository.Path, `{"version":999,"parts":[]}`)
	s := f.sandbox
	s.Original = "" // Public Check has the original repository as its argument.
	return Check(t.Context(), ws, f.root, s)
}

func eligible610(t *testing.T, f *repositoryFixture582) *Checked {
	t.Helper()
	c, err := check610(t, f)
	if err != nil || c == nil || c.Plan == nil || c.Problem != "" {
		t.Fatalf("supported Go draft did not qualify on the independent committed base: %v %+v", err, c)
	}
	if c.Plan.Hash() != f.plan.Hash() || c.Run.Passed() {
		t.Fatal("public planning changed the bound plan or called base failure a candidate pass")
	}
	for _, test := range f.plan.Tests {
		count := 0
		for _, r := range c.Run.TestResults {
			if r.Phase == "accept" && r.identity() == test.Identity() {
				count++
				if r.Status != "failed" || r.Kind != "test" && r.Kind != "compile" {
					t.Fatalf("qualification lacks a completed eligible failure: %+v", r)
				}
			}
		}
		if count != 1 {
			t.Fatalf("want exactly one result for %s, got %d", test.Identity(), count)
		}
	}
	return c
}

func TestConfiguredGoPublicPlanningAndCandidate610(t *testing.T) {
	cache := t.TempDir()
	f := configured610(t, cache, []repositoryCase582{repositoryControl582()}, true)
	c := eligible610(t, f)
	repositoryResult582(t, c.Run, f.plan.Tests[0], "failed", "needs seven")
	if status484(t, c.Run, "api", "test") != "failed" {
		t.Fatal("ordinary base suite failure was hidden")
	}
	f.candidateValue(7)
	r, err := f.sandbox.Run(t.Context(), f.candidate, c.Plan)
	if err != nil || !r.Passed() {
		t.Fatalf("production-only correction did not pass default configured Go: %v\n%s", err, r.Summary())
	}
	repositoryResult582(t, r, c.Plan.Tests[0], "passed", "real completion 582")
	for _, phase := range repositoryPhases {
		if status484(t, r, "api", phase) != "passed" {
			t.Fatal("candidate omitted an applicable command")
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, f.plan.Tests[0].File)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("planning modified the original checkout")
	}
	repositoryRoundTrip582(t, c.Run)
	repositoryRoundTrip582(t, r)
	// Every identity needs failure: equal labels, exact children and different
	// packages do not let a failing neighbor cover an already passing selector.
	source := repositorySource582(`t.Run("bad",func(t *testing.T){t.Fatal("failed child")});t.Run("good.[x]+",func(t *testing.T){t.Log("passing child")})`, "")
	g := configured610(t, cache, []repositoryCase582{
		{"api", "nested", "TestReady/bad", source},
		{"api", "nested", "TestReady/good.[x]+", source},
		{"api", "other", "TestReady", repositorySource582(`t.Fatal("other package failure")`, "")},
	}, true)
	mixed, err := check610(t, g)
	if err != nil || mixed == nil || mixed.Problem == "" {
		t.Fatalf("pass plus fail became proposable: %v %+v", err, mixed)
	}
	for i, status := range []string{"failed", "passed", "failed"} {
		repositoryResult582(t, mixed.Run, g.plan.Tests[i], status, "")
	}
	mixed.Run.Output = strings.Repeat("noise\n", 3000)
	mixed.Run.Output = mixed.Run.Tail(16)
	for _, test := range g.plan.Tests {
		if !strings.Contains(mixed.Feedback(), test.Identity()) {
			t.Fatalf("planning feedback lost selector identity when diagnostics were clipped: %s", mixed.Feedback())
		}
	}
}

func TestConfiguredGoPublicCompilerAttribution610(t *testing.T) {
	cache := t.TempDir()
	f := configured610(t, cache, []repositoryCase582{compilerCase604("nested")}, true)
	c := eligible610(t, f)
	compilerRecord604(t, c.Run, c.Plan, c.Plan.Tests[0], "Future604")
	compilerFix604(t, f)
	r, err := f.sandbox.Run(t.Context(), f.candidate, c.Plan)
	if err != nil || !r.Passed() {
		t.Fatalf("new API implementation needs a fresh runtime pass: %v\n%s", err, r.Summary())
	}
	repositoryResult582(t, r, c.Plan.Tests[0], "passed", "new API executed 604")
	source := "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){if Future610()!=7{t.Fatal(\"api\")}}\nfunc TestNeighbor(t *testing.T){}\n"
	g := configured610(t, cache, []repositoryCase582{
		{"api", "nested", "TestReady", source},
		{"api", "nested", "TestNeighbor", source},
		{"api", "other", "TestReady", repositorySource582(`t.Fatal("independent failure")`, "")},
	}, true)
	checked, _ := check610(t, g)
	if checked == nil || checked.Problem == "" {
		t.Fatal("neighbor borrowed another selector's proposed-API error")
	}
	compilerRecord604(t, checked.Run, g.plan, g.plan.Tests[0], "Future610")
	compilerNoCompletion604(t, checked.Run, g.plan.Tests[1])
	repositoryResult582(t, checked.Run, g.plan.Tests[2], "failed", "independent failure")
}

func TestConfiguredGoPublicRefusalsAndVetoes610(t *testing.T) {
	cache := t.TempDir()
	eligible610(t, configured610(t, cache, []repositoryCase582{repositoryControl582()}, false))
	for _, tc := range []struct{ name, selector, body, extra, status string }{
		{"skip", "TestReady", `t.Skip("not evidence")`, "", "skipped"},
		{"absent", "TestReady/missing", `t.Run("present",func(t *testing.T){t.Fatal("neighbor")})`, "", "not_run"},
		{"unfinished", "TestReady", `fmt.Println("--- FAIL: TestReady\n@@invariant accept=1");os.Exit(19)`, "", "running"},
		{"no execution", "TestReady", `t.Fatal("unreached")`, `func TestMain(m *testing.M){os.Exit(0)}`, "not_run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := configured610(t, cache, []repositoryCase582{{"api", "nested", tc.selector, repositorySource582(tc.body, tc.extra)}}, false)
			c, _ := check610(t, f)
			if c == nil || c.Problem == "" {
				t.Fatal("ineligible execution qualified")
			}
			repositoryResult582(t, c.Run, f.plan.Tests[0], tc.status, "")
			for key := range c.Run.Accepted {
				c.Run.Accepted[key] = true
			}
			if c.Run.Passed() {
				t.Fatal("Accepted map supplied missing completion")
			}
		})
	}
	for _, stage := range []string{"install", "open", "start", "close", "deadline", "cancellation", "mismatched stream", "duplicate terminal", "transport"} {
		t.Run(stage, func(t *testing.T) {
			f := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.runner.before = func(ctx context.Context, got, part string, argv []string) error {
				if stage == "cancellation" && got == "wait" {
					cancel()
					return ctx.Err()
				}
				if stage == "deadline" && got == "open" {
					return context.DeadlineExceeded
				}
				if got == stage {
					return fmt.Errorf("610 %s infrastructure veto", stage)
				}
				return nil
			}
			f.runner.damage = func(_ string, out RepositoryProcessOutput) (RepositoryProcessOutput, error) {
				if stage == "transport" {
					return out, errors.New("610 transport veto")
				}
				if stage == "mismatched stream" || stage == "duplicate terminal" {
					events, err := decodeGoTesting(out.Events)
					if err != nil {
						return out, err
					}
					if stage == "mismatched stream" {
						for i := range events {
							events[i].Name = "TestWrongIdentity"
						}
					} else {
						events = append(events, events[len(events)-1])
					}
					out.Events = repositoryWire582(events)
				}
				return out, nil
			}
			c, err := Check(ctx, n503Draft(t, f.plan), f.root, f.sandbox)
			if c == nil || c.Problem == "" || c.Run.Passed() {
				t.Fatalf("infrastructure failure did not veto planning: %v %+v", err, c)
			}
			if len(c.Run.TestResults) != 1 {
				t.Fatal("veto lost unfinished identity")
			}
			if stage == "close" || stage == "transport" {
				repositoryResult582(t, c.Run, f.plan.Tests[0], "failed", "needs seven")
			}
		})
	}
	t.Run("ordinary command failures supply no evidence", func(t *testing.T) {
		c := repositoryCase582{"api", "nested", "TestReady", repositorySource582(`t.Skip("no failure evidence")`, "")}
		f := newRepositoryFixture582(t, cache, []repositoryCase582{c}, true, map[string]string{"api/unrelated/broken.go": "package unrelated\nvar Bad=Missing610\n"}, nil)
		f.sandbox.Repository.Adapters = nil
		checked, _ := check610(t, f)
		if checked == nil || checked.Problem == "" || status484(t, checked.Run, "api", "build") != "failed" || status484(t, checked.Run, "api", "test") != "failed" {
			t.Fatal("ordinary command failure qualified acceptance or blocked independent execution")
		}
		repositoryResult582(t, checked.Run, f.plan.Tests[0], "skipped", "no failure evidence")
	})
	for _, framework := range []string{"pytest", "vitest", "jest"} {
		// Pytest's native-runner wrapper remains unsupported even though its
		// direct captured commands now have a production adapter.
		for _, mixed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/mixed=%v", framework, mixed), func(t *testing.T) {
				lang, file, selector, source := "typescript", "app/ready.test.ts", "outer ready one", "// pending\n"
				if framework == "pytest" {
					lang, file, selector, source = "python", "app/test_ready.py", "test_ready.py::test_ready", "def test_ready():\n    assert False\n"
				}
				goPart := n503Part("go", "api")
				goPart.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}"}}
				root, candidate, snap := fixture484(t, []repository.Part{goPart, n503Part(lang, "app")}, nil)
				p := n503Plan(t, framework, "app", file, selector, source)
				if mixed {
					goFile := "api/ready_accept_test.go"
					p.Files = append(p.Files, "api/main.go")
					p.Tests = append([]Test{{Name: "Go", Part: "api", Framework: "go", File: goFile, Selector: "TestReady", Says: "Fail independently."}}, p.Tests...)
					p.Sources[goFile] = "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Fatal(\"pending\")}\n"
				}
				p = n503Read(t, p, snap)
				runner := newRunner484(t)
				s := sandbox484(t, root, snap, runner)
				c, _ := Check(t.Context(), n503Draft(t, p), root, s)
				r, _ := s.Run(t.Context(), candidate, p)
				if c == nil || c.Problem == "" || r.Passed() || len(runner.installs)+len(runner.inspections)+len(runner.opens) != 0 {
					t.Fatal("unsupported or mixed plan partly executed or qualified")
				}
			})
		}
	}
	f := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
	legacy := cloneRepositoryPlan(f.plan)
	legacy.Execution = nil
	legacy.Tests[0].Name = legacy.Tests[0].Selector
	legacy.Tests[0].Part, legacy.Tests[0].Framework, legacy.Tests[0].Selector = "", "", ""
	c, _ := Check(t.Context(), n503Draft(t, legacy), f.root, f.sandbox)
	r, _ := f.sandbox.Run(t.Context(), f.candidate, legacy)
	if c == nil || c.Problem == "" || r.Passed() || len(f.runner.observed) != 0 {
		t.Fatal("configured legacy acceptance was executed or silently migrated")
	}
	t.Run("legacy hashes and locks unchanged", TestNativeExtensionPreservesPreChangeGoHashesAndCommittedLocks)
}

func TestConfiguredGoDirectAuthorityAndStaging610(t *testing.T) {
	cache := t.TempDir()
	f := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
	f.candidateValue(7)
	write468(t, f.candidate, repository.Path, `{"version":999,"parts":[]}`)
	r, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
	if err != nil || !r.Passed() {
		t.Fatalf("captured default Go authority unavailable: %v\n%s", err, r.Summary())
	}
	for _, mode := range []string{"missing", "duplicate", "compile", "identity"} {
		bad := r
		bad.TestResults = append([]TestResult(nil), r.TestResults...)
		switch mode {
		case "missing":
			bad.TestResults = nil
		case "duplicate":
			bad.TestResults = append(bad.TestResults, bad.TestResults[0])
		case "compile":
			bad.TestResults[0].Kind = "compile"
		case "identity":
			bad.TestResults[0].Selector = "TestReplacement"
		}
		if bad.Passed() {
			t.Fatalf("%s evidence plus suite/Accepted success manufactured a candidate pass", mode)
		}
	}
	for _, mode := range []string{"execution", "identity", "source", "input", "lockfile"} {
		t.Run(mode, func(t *testing.T) {
			g := configured610(t, cache, []repositoryCase582{repositoryControl582()}, false)
			g.candidateValue(7)
			switch mode {
			case "execution":
				g.plan.Execution.Digest = hash468("replacement policy")
			case "identity":
				g.plan.Tests[0].Selector = "TestReady/replaced"
			case "source":
				write468(t, g.candidate, g.plan.Tests[0].File, g.plan.Sources[g.plan.Tests[0].File]+"// changed bytes\n")
			case "input":
				write468(t, g.candidate, "api/go.mod", "module example.invalid/api582\n\ngo 1.27.1\n// changed captured bytes\n")
			case "lockfile":
				write468(t, g.candidate, "api/go.sum", "\n")
			}
			r, _ := g.sandbox.Run(t.Context(), g.candidate, g.plan)
			if r.Passed() || len(g.runner.observed) != 0 {
				t.Fatalf("changed %s reached preparation or execution", mode)
			}
		})
	}
	lock := Lock{Plan: *f.plan, Ratified: Ratification{Issue: 610, Proposal: f.plan.Hash()}}
	if err := lock.Write(f.root); err != nil {
		t.Fatal(err)
	}
	write468(t, f.candidate, "api/nested/unplanned.go", "package fixture\nvar Unplanned=Missing610\n")
	write468(t, f.candidate, "api/nested/extra_test.go", "package fixture\nimport \"testing\"\nfunc TestRegression610(t *testing.T){if Value!=7{t.Fatal(Value)}}\n")
	write468(t, f.candidate, f.plan.Tests[0].File, "package fixture\n// candidate cannot replace acceptance\n")
	dst := t.TempDir()
	if err := Stage(t.Context(), f.root, f.candidate, f.plan, dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api/nested/value.go", "api/nested/extra_test.go"} {
		if read484(t, dst, name) != read484(t, f.candidate, name) {
			t.Fatal("lost authorized production or Go regression")
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "api/nested/unplanned.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unplanned production entered staged checks")
	}
	if read484(t, dst, repository.Path) != read484(t, f.root, repository.Path) || read484(t, dst, f.plan.Tests[0].File) != f.plan.Sources[f.plan.Tests[0].File] {
		t.Fatal("candidate replaced captured policy or acceptance")
	}
	r, err = f.sandbox.Run(t.Context(), dst, f.plan)
	if err != nil || !r.Passed() {
		t.Fatalf("authorized staging failed: %v\n%s", err, r.Summary())
	}
}
