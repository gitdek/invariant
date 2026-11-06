package plumbing

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func part617(name, image string) repository.Part {
	commands := map[string]repository.Command{}
	for _, phase := range []string{"install", "format", "lint", "build", "test"} {
		commands[phase] = repository.Command{NotApplicable: "Already prepared offline fixture; no command for this phase."}
	}
	commands["test_one"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "--import-mode=importlib", "-q", "{test}"}}
	return repository.Part{Name: name, Directory: name, Language: "python", Image: image, Inputs: []string{}, Lockfiles: []string{}, Commands: commands}
}

func adapter617(t *testing.T) RepositoryAcceptanceAdapter {
	t.Helper()
	a := (Sandbox{Repository: &RepositoryChecks{}}).repositoryAdapter("pytest")
	if a == nil {
		t.Fatal("captured Python acceptance has no default pytest adapter")
	}
	return a
}

func selection617(t *testing.T) {
	t.Helper()
	p := part617("worker", "python@"+hash468("pinned617"))
	test := Test{Name: "display only", Part: "worker", Framework: "pytest", File: "worker/nested/test_exact.py", Selector: "nested/test_exact.py::TestExact::test_value[a b;$(literal).+|x]"}
	got, err := adapter617(t).Select(t.Context(), p, test)
	if err != nil || !slices.Contains(got.Argv, test.Selector) || slices.Contains(got.Argv, "-k") {
		t.Fatalf("literal pytest selection unavailable: %v %+v", err, got)
	}
}

func TestPytestLiteralSelection617(t *testing.T) {
	a := adapter617(t)
	p := part617("worker", "python@"+hash468("pinned617"))
	test := Test{Name: "display only", Part: "worker", Framework: "pytest", File: "worker/nested/test_exact.py", Selector: "nested/test_exact.py::TestExact::test_value[a b;$(literal).+|x]"}
	for _, argv := range [][]string{
		{"pytest", "{test}"},
		{"pytest", "-s", "{test}"},
		{"python3", "-m", "pytest", "-q", "--tb=short", "--import-mode=importlib", "{test}"},
		{"pytest", "-p", "fixture_plugin", "-c", "pytest.ini", "--strict-markers", "{test}"},
	} {
		p.Commands["test_one"] = repository.Command{Argv: slices.Clone(argv)}
		want := slices.Clone(argv)
		for i := range want {
			if want[i] == "{test}" {
				want[i] = test.Selector
			}
		}
		got, err := a.Select(t.Context(), p, test)
		if err != nil || !reflect.DeepEqual(got.Argv, want) {
			t.Fatalf("captured policy or literal node changed: %v got=%q want=%q", err, got.Argv, want)
		}
		got.Argv[0] = "mutated"
		if !reflect.DeepEqual(p.Commands["test_one"].Argv, argv) {
			t.Fatal("selection aliases captured arguments")
		}
	}
	for _, argv := range [][]string{
		{"sh", "-c", "pytest {test}"}, {"uv", "run", "pytest", "{test}"},
		{"python3", "-c", "import pytest; pytest.main(['{test}'])"},
		{"pytest", "-k", "{test}"}, {"pytest", "-m", "{test}"},
		{"pytest", "{test}", "other.py"}, {"pytest", "{test}", "{test}"},
		{"pytest", "prefix{test}"}, {"pytest", "--collect-only", "{test}"},
		{"pytest", "-n", "2", "{test}"}, {"pytest", "--pyargs", "{test}"},
		{"pytest", "--deselect", "{test}"}, {"pytest", "--override-ini", "addopts=-k other", "{test}"},
		{"pytest", "-c", "../outside.ini", "{test}"}, {"pytest", "--unknown", "{test}"},
	} {
		p.Commands["test_one"] = repository.Command{Argv: argv}
		if _, err := a.Select(t.Context(), p, test); err == nil {
			t.Errorf("unsafe template accepted: %q", argv)
		}
	}
	p = part617("worker", "python@"+hash468("pinned617"))
	for _, selector := range []string{"nested/test_exact.py", "nested/test_exact.py::TestExact", "other.py::test_value", "../test_exact.py::test_value"} {
		bad := test
		bad.Selector = selector
		if _, err := a.Select(t.Context(), p, bad); err == nil {
			t.Errorf("non-exact or wrong-file selection accepted: %s", selector)
		}
	}
	bad := test
	bad.Part = "other"
	if _, err := a.Select(t.Context(), p, bad); err == nil {
		t.Fatal("selection escaped captured owner")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Select(ctx, p, test); err == nil {
		t.Fatal("canceled selection succeeded")
	}
	override := &adapter515{}
	s := Sandbox{Repository: &RepositoryChecks{Adapters: map[string]RepositoryAcceptanceAdapter{"pytest": override}}}
	if s.repositoryAdapter("pytest") != override || s.repositoryAdapter("go") == nil || s.repositoryAdapter("vitest") == nil || s.repositoryAdapter("jest") == nil {
		t.Fatal("default pytest changed trusted overrides or remaining framework support")
	}
}

func TestPytestNeedsEvidenceBeyondConsole617(t *testing.T) {
	selection617(t)
	p := part617("worker", "python@"+hash468("pinned617"))
	root, candidate, snapshot := fixture484(t, []repository.Part{p}, nil)
	plan := n503Plan(t, "pytest", "worker", "worker/test_ready.py", "test_ready.py::test_ready", "def test_ready():\n    assert False\n")
	plan.Files = []string{"worker/main.py"}
	plan = n503Read(t, plan, snapshot)
	runner := newRunner484(t)
	runner.hook = func(context.Context, DependencyRequest, []string) ([]byte, error) {
		return []byte("1 passed\n1 failed\n@@invariant accept=0\n{\"status\":\"failed\"}\n"), nil
	}
	s := sandbox484(t, root, snapshot, runner)
	c, err := Check(t.Context(), n503Draft(t, plan), root, s)
	if err != nil || c == nil || c.Problem == "" || len(runner.opens) == 0 {
		t.Fatalf("supported pytest must prepare but refuse missing evidence: %v %+v", err, c)
	}
	r, _ := s.Run(t.Context(), candidate, plan)
	if r.Passed() || len(runner.closed) != len(runner.opens) {
		t.Fatal("console-only environment qualified or leaked")
	}
	for _, record := range r.TestResults {
		if record.Phase == "accept" && (record.Status == "failed" || record.Status == "passed") {
			t.Fatal("diagnostics manufactured completion")
		}
	}
}

func TestPytestPreparedPythonCapability617(t *testing.T) {
	_, logs := docker484(t) // Only Docker's CLI is replaced; no pytest verdict is faked.
	for _, language := range []string{"python", "typescript"} {
		e, err := (&DockerDependencyRunner{}).OpenChecks(t.Context(), request484(t, language))
		if err != nil {
			t.Fatal(err)
		}
		_, process := e.(RepositoryProcessTransport)
		_, acceptance := e.(RepositoryAcceptanceEnvironment)
		if !process || !acceptance {
			_ = e.Close(t.Context())
			t.Fatalf("wrong prepared capability for %s: process=%v acceptance=%v", language, process, acceptance)
		}
		if out, err := e.Run(t.Context(), []string{"fixture-check"}); err != nil || !strings.Contains(string(out), "container diagnostic") {
			t.Fatalf("ordinary commands changed: %v %s", err, out)
		}
		close484(t, e)
	}
	for _, argv := range invocations484(t, logs) {
		for _, arg := range argv {
			if strings.Contains(arg, "/invariant/process-") || arg == "pip" || arg == "pytest" || arg == "go" {
				t.Fatalf("ordinary Python/TypeScript command staged or installed a runtime helper: %q", argv)
			}
		}
	}
}
