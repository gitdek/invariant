package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

const projectDriver779 = "import {value} from '../../src/app.ts';\nvoid value;\n"

func projectsFixture779(t *testing.T, language, dir, projects string, explicit bool) (string, string, *repository.Snapshot, repository.Part, map[string]string) {
	t.Helper()
	p := part484(language, dir)
	p.Name = "app"
	files := files468(language, dir)
	keep := map[string]string{
		path.Join(dir, "src/app.ts"):              "export const value = 7;\n",
		path.Join(dir, "src/invariant/helper.ts"): "export const applicationHelper = true;\n",
		projects + "-tools/helper.ts":             "export const neighbor = true;\n",
	}
	for name, body := range keep {
		files[name] = body
	}
	files[path.Join(projects, "01-compile-once/conformance.ts")] = projectDriver779
	files[path.Join(projects, "01-compile-once/.invariant/invariant.json")] = `{"name":"compile once","module":"spec.tla","language":"typescript","code":"conformance.ts"}`
	files[path.Join(projects, "shared/helper.ts")] = "export const projectHelper = true;\n"
	if explicit {
		files["invariant/ordinary.ts"] = "export const ordinary = 1;\n"
		c := repository.Config{Version: 1, Parts: []repository.Part{p}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatal(err)
		}
		raw["projects_directory"] = projects
		b, err = json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		files[repository.Path] = string(b)
	}
	root, snapshot := snapshot468(t, []repository.Part{p}, files)
	candidate := t.TempDir()
	if err := CopyCheckout(t.Context(), root, candidate); err != nil {
		t.Fatal(err)
	}
	return root, candidate, snapshot, p, keep
}

func projectsView779(q DependencyRequest, projects string, keep map[string]string) error {
	if _, err := os.Lstat(filepath.Join(q.SourceDir, filepath.FromSlash(projects))); !os.IsNotExist(err) {
		return fmt.Errorf("repository checks can still see projects directory %s: %v", projects, err)
	}
	for name, want := range keep {
		b, err := os.ReadFile(filepath.Join(q.SourceDir, filepath.FromSlash(name)))
		if err != nil || string(b) != want {
			return fmt.Errorf("application file %s was lost or rewritten: %q, %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(q.SourceDir, repository.Path)); err != nil {
		return err
	}
	return nil
}

func assertProjectsRetained779(t *testing.T, projects string, roots ...string) {
	t.Helper()
	for _, root := range roots {
		if got := read484(t, root, path.Join(projects, "01-compile-once/conformance.ts")); got != projectDriver779 {
			t.Errorf("driver imports changed in %s: %q", root, got)
		}
	}
}

func TestConfiguredCommandsExcludeProjects779(t *testing.T) {
	for _, language := range []string{"typescript", "python", "go"} {
		t.Run(language, func(t *testing.T) {
			root, candidate, snapshot, part, keep := projectsFixture779(t, language, ".", "invariant", false)
			runner := newRunner484(t)
			runner.openHook = func(_ context.Context, q DependencyRequest) error { return projectsView779(q, "invariant", keep) }
			sandbox := sandbox484(t, root, snapshot, runner)
			plan := &Plan{Files: []string{"src/app.ts"}}
			result, err := sandbox.Run(t.Context(), candidate, plan)
			if err != nil || !result.Passed() {
				t.Fatalf("application checks must succeed independently of project drivers: %v\n%s", err, result.Summary())
			}
			if len(runner.calls) != 4 {
				t.Fatalf("lost ordinary checks: %+v", runner.calls)
			}
			for i, phase := range phases484 {
				if !slices.Equal(runner.calls[i].argv, part.Commands[phase].Argv) || status484(t, result, "app", phase) != "passed" {
					t.Errorf("%s command or outcome changed: %+v", phase, runner.calls[i])
				}
			}
			assertProjectsRetained779(t, "invariant", root, candidate)
			closed484(t, runner)
			// An application build failure must still fail this same path.
			runner.hook = func(_ context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
				if slices.Equal(argv, part.Commands["build"].Argv) {
					return []byte("application type error"), errors.New("exit status 7")
				}
				return nil, nil
			}
			result, err = sandbox.Run(t.Context(), candidate, plan)
			if err != nil || result.Passed() || status484(t, result, "app", "build") != "failed" || !strings.Contains(result.Output, "application type error") {
				t.Fatalf("application failure was suppressed: %v\n%s", err, result.Summary())
			}
			closed484(t, runner)
		})
	}
}

func TestConfiguredCustomProjectsUseCapturedAuthority779(t *testing.T) {
	projects := "web/model checks"
	root, candidate, snapshot, _, keep := projectsFixture779(t, "typescript", "web", projects, true)
	// Candidate policy is deliberately different. The original contract owns
	// the exclusion; the candidate may not use it to conceal web/src.
	b := []byte(read484(t, candidate, repository.Path))
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	c["projects_directory"] = "web/src"
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, candidate, repository.Path, string(b))
	write468(t, candidate, "invariant/ordinary.ts", "export const ordinary = 1;\n")
	keep["invariant/ordinary.ts"] = "export const ordinary = 1;\n"
	runner := newRunner484(t)
	runner.openHook = func(_ context.Context, q DependencyRequest) error { return projectsView779(q, projects, keep) }
	sandbox := sandbox484(t, root, snapshot, runner)
	result, err := sandbox.Run(t.Context(), candidate, &Plan{Files: []string{repository.Path, "web/src/app.ts"}})
	if err != nil || !result.Passed() {
		t.Fatalf("captured custom directory was not isolated: %v\n%s", err, result.Summary())
	}
	assertProjectsRetained779(t, projects, root, candidate)
	if got := read484(t, candidate, repository.Path); got != string(b) {
		t.Fatal("checks rewrote candidate configuration")
	}
	closed484(t, runner)
	// Omission cannot bypass the regular-file staging rules, even for files
	// below the projects directory that checks would otherwise not see.
	if err := os.Symlink("../../src/app.ts", filepath.Join(candidate, filepath.FromSlash(projects), "linked.ts")); err != nil {
		t.Fatal(err)
	}
	before := len(runner.opens)
	result, err = sandbox.Run(t.Context(), candidate, &Plan{Files: []string{"web/src/app.ts"}})
	if err == nil || result.Passed() || len(runner.opens) != before {
		t.Fatalf("excluded tree bypassed safe staging: %v %+v", err, result)
	}
}

func TestNativeChecksExcludeProjectsOnBaseAndCandidate779(t *testing.T) {
	f := newFixture515(t)
	for _, root := range []string{f.root, f.candidate} {
		write468(t, root, "invariant/01-compile-once/conformance.ts", projectDriver779)
		write468(t, root, "invariant/01-compile-once/.invariant/invariant.json", "{}\n")
	}
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-q", "--no-verify", "-m", "modeled project")
	var err error
	f.snapshot, err = repository.Load(t.Context(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.plan = n503Read(t, f.plan, f.snapshot)
	f.sandbox.Repository.Snapshot = f.snapshot
	f.runner.openHook = func(_ context.Context, q DependencyRequest) error {
		return projectsView779(q, "invariant", map[string]string{"api/main.go": "package fixture\n"})
	}
	allFail515(f.runner)
	checked := base515(t, f, t.Context())
	if checked.Problem != "" {
		t.Fatalf("independent base failures were blocked by project files: %s", checked.Problem)
	}
	f.runner.accept = nil
	result, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
	if err != nil || !result.Passed() {
		t.Fatalf("native candidate checks were blocked by project files: %v\n%s", err, result.Summary())
	}
	if len(f.runner.invocations) != 4 || len(f.runner.opens) != 6 {
		t.Fatalf("lost separate commands or native invocations: opens=%d accepts=%d", len(f.runner.opens), len(f.runner.invocations))
	}
	assertBindings515(t, f)
	assertProjectsRetained779(t, "invariant", f.root, f.candidate)
	closed484(t, f.runner.runner484)
}

func TestProjectDirectoryCannotHideAcceptanceOrRegressions779(t *testing.T) {
	for _, framework := range []string{"vitest", "jest", "pytest", "go"} {
		t.Run(framework, func(t *testing.T) {
			language, file, selector, source := "typescript", "new.test.ts", "works", "import {test,expect} from 'vitest';\ntest('works',()=>expect(1).toBe(2));\n"
			suite, one := []string{"vitest", "run"}, []string{"vitest", "run", "-t", "{test}"}
			switch framework {
			case "jest":
				suite, one = []string{"jest"}, []string{"jest", "-t", "{test}"}
				source = strings.ReplaceAll(source, "'vitest'", "'@jest/globals'")
			case "pytest":
				language, file, selector, source = "python", "test_new.py", "invariant/test_new.py::test_works", "def test_works():\n    assert False\n"
				suite, one = []string{"pytest"}, []string{"pytest", "{test}"}
			case "go":
				language, file, selector, source = "go", "new_test.go", "TestWorks", "package fixture\nimport \"testing\"\nfunc TestWorks(t *testing.T) { t.Fatal(\"missing\") }\n"
				suite, one = []string{"go", "test", "./..."}, []string{"go", "test", "./...", "-run", "{test}"}
			}
			p := part484(language, ".")
			p.Name = "app"
			p.Commands["test"], p.Commands["test_one"] = command468(suite...), command468(one...)
			files := files468(language, ".")
			files["src/app.ts"] = "export const value = 1;\n"
			files["invariant/main.go"] = "package fixture\n"
			files["invariant-tools/main.go"] = "package fixture\n"
			root, snapshot := snapshot468(t, []repository.Part{p}, files)
			plan := n503Plan(t, framework, "app", "invariant/"+file, selector, source)
			plan.Files = []string{"src/app.ts"}
			if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, plan), snapshot); err == nil || !strings.Contains(strings.ToLower(err.Error()), "project") {
				t.Errorf("acceptance under excluded projects must be refused explicitly: %v", err)
			}
			regressions := &Plan{Files: []string{"src/app.ts", "invariant/main.go", "invariant-tools/main.go"}}
			scope, err := checkoutRegressionScope(t.Context(), root, regressions)
			if err != nil {
				t.Fatal(err)
			}
			got := scope.Files([]string{"invariant/" + file, "invariant-tools/" + file})
			if !slices.Equal(got, []string{"invariant-tools/" + file}) {
				t.Errorf("excluded projects gained implicit tests, or a neighboring directory lost them: %q", got)
			}
		})
	}
}
