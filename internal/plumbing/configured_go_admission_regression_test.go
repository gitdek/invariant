package plumbing

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestConfiguredGoEmptyInstallationRetainsInputsAndRefusesDependencies(t *testing.T) {
	cache := t.TempDir()
	for _, mode := range []string{"empty graph", "requirement", "extra input", "nonempty sums"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string]string{}
			if mode == "requirement" {
				files["api/go.mod"] = "module example.invalid/api582\n\ngo 1.27.1\nrequire example.invalid/dep v1.0.0\n"
			}
			if mode == "requirement" || mode == "nonempty sums" {
				files["api/go.sum"] = "example.invalid/dep v1.0.0 h1:" + strings.Repeat("A", 43) + "=\nexample.invalid/dep v1.0.0/go.mod h1:" + strings.Repeat("A", 43) + "=\n"
			}
			if mode == "extra input" {
				files["api/settings.txt"] = "undeclared installation semantics\n"
			}
			f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, files, func(c *repository.Config) {
				c.Parts[0].Commands["install"] = repository.Command{NotApplicable: "No external dependencies."}
				if mode == "extra input" {
					c.Parts[0].Inputs = append(c.Parts[0].Inputs, "api/settings.txt")
				}
			})
			f.sandbox.Repository.Adapters = nil
			if mode != "empty graph" {
				c, _ := check610(t, f)
				if c == nil || c.Problem == "" || len(f.runner.observed) != 0 {
					t.Fatal("inapplicable installation bypassed declared dependency policy")
				}
				return
			}
			eligible610(t, f)
			f.candidateValue(7)
			r, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
			if err != nil || !r.Passed() || len(f.runner.installs) != 1 || len(f.runner.inspections) < 2 {
				t.Fatalf("empty environment was not verified and reused: %v %s", err, r.Summary())
			}
			q := f.runner.installs[0]
			if q.Network || len(q.Argv) != 0 || len(q.Inputs) != 2 || q.Inputs["api/go.mod"] != hash468(read484(t, f.root, "api/go.mod")) || q.Inputs["api/go.sum"] != hash468("") {
				t.Fatal("empty installation lost captured bytes or became online")
			}
			write468(t, f.candidate, "api/go.mod", read484(t, f.root, "api/go.mod")+"// changed authority\n")
			observed := len(f.runner.observed)
			r, _ = f.sandbox.Run(t.Context(), f.candidate, f.plan)
			if r.Passed() || len(f.runner.observed) != observed {
				t.Fatal("empty installation allowed replacement inputs")
			}
		})
	}
}

func TestConfiguredPlanningUsesCapturedCommitDespiteWorkingTreeEdits(t *testing.T) {
	f := configured610(t, t.TempDir(), []repositoryCase582{repositoryControl582()}, false)
	write468(t, f.root, "api/nested/value.go", "package fixture\nconst Value=7\n")
	write468(t, f.root, "api/nested/untracked.go", "package fixture\nvar Poison=MissingUntracked\n")
	write468(t, f.root, f.plan.Tests[0].File, "package fixture\n// untracked acceptance is not the captured base\n")
	c := eligible610(t, f)
	repositoryResult582(t, c.Run, f.plan.Tests[0], "failed", "needs seven")
	if !strings.Contains(c.Feedback(), f.plan.Tests[0].Identity()) || !strings.Contains(c.Feedback(), "needs seven") {
		t.Fatal("successful planning feedback lost completed evidence")
	}
	if got := read484(t, f.root, "api/nested/value.go"); !strings.Contains(got, "Value=7") {
		t.Fatal("checking the captured base rewrote the working tree")
	}
}

func TestConfiguredRuntimePassRequiresUniqueExpectedIdentity(t *testing.T) {
	test := Test{Name: "ready", Part: "api", Framework: "go", File: "api/a_test.go", Selector: "TestReady"}
	control := unstartedAcceptance(&Plan{Tests: []Test{test}})
	control.TestResults[0].Status, control.TestResults[0].Kind = "passed", "test"
	if !control.Passed() {
		t.Fatal("runtime evidence depended on the Accepted boolean")
	}
	for _, mode := range []string{"duplicate", "unexpected", "compile", "no kind", "missing"} {
		t.Run(mode, func(t *testing.T) {
			r := control
			r.Accepted = map[string]bool{test.Identity(): true}
			r.TestResults = append([]TestResult(nil), control.TestResults...)
			switch mode {
			case "duplicate":
				r.TestResults = append(r.TestResults, r.TestResults[0])
			case "unexpected":
				other := r.TestResults[0]
				other.Selector = "TestOther"
				r.TestResults = append(r.TestResults, other)
			case "compile":
				r.TestResults[0].Kind = "compile"
			case "no kind":
				r.TestResults[0].Kind = ""
			case "missing":
				r.TestResults = nil
			}
			if r.Passed() {
				t.Fatal("suite and Accepted success supplied valid runtime completion")
			}
		})
	}
}
