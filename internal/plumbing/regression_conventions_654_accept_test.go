package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

// These fixtures use the existing trusted command/evidence fake. They check
// the bytes and literal argv delivered to the suite, independently of native
// acceptance evidence; no framework, Docker daemon or network is needed.
type convention654 struct {
	framework, language, source, before, after string
	accept, selector, acceptance               string
	config, configText                         string
	added, refused                             []string
	suite, one                                 []string
}

func conventions654() []convention654 {
	return []convention654{
		{
			framework: "pytest", language: "python", source: "app/src/orders/service.py", before: "value = 1\n", after: "value = 2\n",
			accept: "app/tests/orders/test_contract.py", selector: "tests/orders/test_contract.py::test_contract", acceptance: "def test_contract():\n    assert True\n",
			config: "app/pytest.ini", configText: "[pytest]\ntestpaths = tests src\npython_files = test_*.py *_test.py\nnorecursedirs = excluded\n",
			added:   []string{"app/tests/orders/test_regression.py", "app/tests/billing/refund_test.py", "app/src/orders/test_service.py"},
			refused: []string{"app/tests/excluded/test_hidden.py", "app/outside/test_hidden.py", "app/tests/helper.py", "app/tests/conftest.py", "app/tests/__init__.py", "app/tests/test_fake.py.txt", "other/tests/test_other.py", "app/vendor/tests/test_nested.py"},
			suite:   []string{"python3", "-m", "pytest", "-q", "tests", "src"}, one: []string{"python3", "-m", "pytest", "-q", "{test}"},
		},
		{
			framework: "vitest", language: "typescript", source: "app/src/orders/service.ts", before: "export const value = 1;\n", after: "export const value = 2;\n",
			accept: "app/tests/orders/contract.test.ts", selector: "contract", acceptance: "import {test,expect} from 'vitest';\ntest('contract',()=>expect(1).toBe(1));\n",
			config: "app/vitest.config.ts", configText: "export default {test: {include: ['tests/**/*.test.ts', 'src/**/*.spec.ts'], exclude: ['tests/excluded/**']}};\n",
			added:   []string{"app/tests/orders/regression.test.ts", "app/tests/billing/refund.test.ts", "app/src/orders/service.spec.ts"},
			refused: []string{"app/tests/excluded/hidden.test.ts", "app/outside/hidden.test.ts", "app/tests/helper.ts", "app/tests/fake.test.ts.map", "app/tsconfig.test.json", "other/tests/other.test.ts", "app/vendor/tests/nested.test.ts"},
			suite:   []string{"vitest", "run", "--config", "vitest.config.ts"}, one: []string{"vitest", "run", "--config", "vitest.config.ts", "-t", "{test}"},
		},
		{
			framework: "jest", language: "typescript", source: "app/src/orders/service.ts", before: "export const value = 1;\n", after: "export const value = 2;\n",
			accept: "app/tests/orders/contract.spec.ts", selector: "contract", acceptance: "import {test,expect} from '@jest/globals';\ntest('contract',()=>expect(1).toBe(1));\n",
			config: "app/jest.config.json", configText: `{"testMatch":["<rootDir>/tests/**/*.spec.ts","<rootDir>/src/**/*.test.ts","<rootDir>/__tests__/**/*.ts"],"testPathIgnorePatterns":["/excluded/"]}` + "\n",
			added:   []string{"app/tests/orders/regression.spec.ts", "app/tests/billing/refund.spec.ts", "app/src/orders/service.test.ts", "app/__tests__/refund.ts"},
			refused: []string{"app/tests/excluded/hidden.spec.ts", "app/outside/hidden.spec.ts", "app/tests/helper.ts", "app/tests/fake.spec.ts.map", "app/tsconfig.test.json", "other/tests/other.spec.ts", "app/vendor/tests/nested.spec.ts"},
			suite:   []string{"jest", "--config", "jest.config.json", "--runInBand"}, one: []string{"jest", "--config", "jest.config.json", "--runInBand", "-t", "{test}"},
		},
		{
			framework: "go", language: "go", source: "app/page/web/app.js", before: "const value = 1;\n", after: "const value = 2;\n",
			accept: "app/page/contract_accept_test.go", selector: "TestContract", acceptance: "package page\nimport \"testing\"\nfunc TestContract(t *testing.T) {}\n",
			added:   []string{"app/page/regression_test.go", "app/page/repair_test.go", "app/new/regression_test.go"},
			refused: []string{"app/page/child/regression_test.go", "app/other/regression_test.go", "app/page/regression.go", "other/page/regression_test.go", "app/vendor/regression_test.go"},
			suite:   []string{"go", "test", "./..."}, one: []string{"go", "test", "./page", "-run", "{test}"},
		},
	}
}

func regressionText654(c convention654, file string, fail bool, round int) string {
	if c.framework == "go" {
		pkg := "page"
		if strings.HasPrefix(file, "app/new/") {
			pkg = "newpkg"
		}
		body := ""
		if fail {
			body = `t.Fatal("regression654")`
		}
		name := strings.NewReplacer("/", "_", ".", "_").Replace(file)
		return fmt.Sprintf("package %s\nimport \"testing\"\nfunc TestRegression654_%s(t *testing.T) {%s}\n// round %d\n", pkg, name, body, round)
	}
	if c.framework == "pytest" {
		want := "True"
		if fail {
			want = "False"
		}
		return fmt.Sprintf("def test_regression654():\n    assert %s\n# round %d\n", want, round)
	}
	module := "vitest"
	if c.framework == "jest" {
		module = "@jest/globals"
	}
	want := 1
	if fail {
		want = 2
	}
	return fmt.Sprintf("import {test,expect} from '%s';\ntest('regression654',()=>expect(1).toBe(%d));\n// round %d\n", module, want, round)
}

type fixture654 struct {
	c        convention654
	root     string
	snapshot *repository.Snapshot
	plan     *Plan
	sandbox  Sandbox
	runner   *runner515
	baseline map[string]string
	existing string
}

func newFixture654(t *testing.T, c convention654) *fixture654 {
	t.Helper()
	var parts []repository.Part
	files := map[string]string{}
	for _, dir := range []string{"app", "other", "app/vendor"} {
		p := part468(c.language, dir)
		p.Name = strings.ReplaceAll(dir, "/", "-")
		for _, phase := range []string{"format", "lint", "build"} {
			p.Commands[phase] = command468("fixture-"+phase, ".")
		}
		p.Commands["test"], p.Commands["test_one"] = command468(c.suite...), command468(c.one...)
		parts = append(parts, p)
		for name, source := range files468(c.language, dir) {
			files[name] = source
		}
	}
	files[c.source] = c.before
	if c.config != "" {
		files[c.config] = c.configText
	}
	existing := strings.Replace(c.added[0], "regression", "existing", 1)
	files[existing] = regressionText654(c, existing, false, 0)
	if c.framework == "go" {
		files["app/page/page.go"] = "package page\nimport _ \"embed\"\n//go:embed web/app.js\nvar script string\n"
		files["app/old/old.go"] = "package old\n"
		files["app/other/other.go"] = "package other\n"
	}
	root, snapshot := snapshot468(t, parts, files)
	plan := &Plan{Name: "captured regression scope", Summary: "Change orders and test it under captured conventions.", Files: []string{c.source},
		Tests: []Test{{Name: "contract", Part: "app", Framework: c.framework, File: c.accept, Selector: c.selector, Says: "The contract holds."}}, Sources: map[string]string{c.accept: c.acceptance}}
	if c.framework == "go" {
		plan.Files = append(plan.Files, "app/old/old.go", "app/new/new.go")
	}
	plan = n503Read(t, plan, snapshot)
	if err := (Lock{Plan: *plan, Ratified: Ratification{Issue: 654, Proposal: plan.Hash()}}).Write(root); err != nil {
		t.Fatal(err)
	}
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-q", "--no-verify", "-m", "ratified fixture")
	baseline := map[string]string{}
	for _, name := range append([]string{repository.Path, LockPath(654), c.accept, existing}, c.config) {
		if name != "" {
			baseline[name] = read484(t, root, name)
		}
	}
	r := &runner515{runner484: newRunner484(t)}
	sandbox := sandbox484(t, root, snapshot, r.runner484)
	sandbox.Timeout = 30 * time.Second
	sandbox.Repository.Dependencies.Runner = r
	// Keep actual captured adapter selection; only execution evidence is fake.
	sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{c.framework: &adapter515{selectHook: func(ctx context.Context, p repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
		switch test.Framework {
		case "go":
			return (GoRepositoryAcceptanceAdapter{}).Select(ctx, p, test)
		case "pytest":
			return (PytestRepositoryAcceptanceAdapter{}).Select(ctx, p, test)
		case "vitest":
			return (VitestRepositoryAcceptanceAdapter{}).Select(ctx, p, test)
		default:
			return (JestRepositoryAcceptanceAdapter{}).Select(ctx, p, test)
		}
	}}}
	return &fixture654{c: c, root: root, snapshot: snapshot, plan: plan, sandbox: sandbox, runner: r, baseline: baseline, existing: existing}
}

func readOptional654(root, name string) string {
	b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	return string(b)
}

func verifyProtected654(t *testing.T, f *fixture654, root string) {
	t.Helper()
	for name, want := range f.baseline {
		if got := readOptional654(root, name); got != want {
			t.Errorf("protected %s changed: %q", name, got)
		}
	}
	for _, name := range f.c.refused {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unauthorized file reached candidate/copy-back: %s (%v)", name, err)
		}
	}
}

type agent654 struct {
	t               *testing.T
	f               *fixture654
	rounds, reviews int
	repair, tool    bool
	edit            func(string, int)
	afterTool       func(string)
	inspect         func(string, int)
	toolResults     []Result
}

func (*agent654) Name() string { return "regression convention fixture" }
func (a *agent654) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		a.reviews++
		if a.inspect != nil {
			a.inspect(filepath.Join(job.Workspace, "repo"), a.rounds)
		}
		base := filepath.Join(job.Workspace, "base")
		if got := readOptional654(base, a.f.c.source); got != a.f.c.before {
			a.t.Errorf("review baseline moved: %q", got)
		}
		for _, file := range a.f.c.added {
			if _, err := os.Stat(filepath.Join(base, file)); !errors.Is(err, os.ErrNotExist) {
				a.t.Errorf("review baseline acquired %s", file)
			}
		}
		if a.repair && a.reviews == 1 {
			return synth.Usage{Summary: "VERDICT: changes needed\nCorrect the first added regression and remove the second."}, nil
		}
		return synth.Usage{Summary: "VERDICT: approve"}, nil
	}
	a.rounds++
	write468(a.t, job.Workspace, a.f.c.source, a.f.c.after)
	if a.f.c.framework == "go" {
		if err := os.Remove(filepath.Join(job.Workspace, "app/old/old.go")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return synth.Usage{}, err
		}
		write468(a.t, job.Workspace, "app/new/new.go", "package newpkg\n")
	}
	if a.edit != nil {
		a.edit(job.Workspace, a.rounds)
	}
	if a.tool {
		client := &CheckClient{}
		for i := 0; i+1 < len(job.GateServer); i++ {
			switch job.GateServer[i] {
			case "-check-queue":
				client.Address = job.GateServer[i+1]
			case "-check-build":
				client.Build = job.GateServer[i+1]
			}
		}
		if client.Address == "" || client.Build == "" {
			return synth.Usage{}, errors.New("configured build omitted check endpoint")
		}
		result, err := (Sandbox{Checks: client}).Run(ctx, job.Workspace, &Plan{})
		if err != nil {
			return synth.Usage{}, err
		}
		a.toolResults = append(a.toolResults, result)
	}
	if a.afterTool != nil {
		a.afterTool(job.Workspace)
	}
	return synth.Usage{Summary: "candidate written"}, nil
}

func build654(t *testing.T, f *fixture654, a *agent654, shared bool) (*BuildResult, error) {
	t.Helper()
	b := Builder{Backend: a, Reviewer: a, Binary: "unused", TestRuns: 4, Timeout: time.Minute, Sandbox: f.sandbox}
	if shared {
		q, err := NewCheckQueue(1)
		if err != nil {
			t.Fatal(err)
		}
		defer q.Close()
		b.Checks = q
	}
	return b.Build(t.Context(), f.root, 654, t.TempDir())
}

func phase654(t *testing.T, f *fixture654, q DependencyRequest, argv []string) string {
	t.Helper()
	if q.Directory != "app" {
		t.Errorf("an unrelated part ran: %q", q.Directory)
	}
	for _, p := range f.snapshot.Config().Parts {
		if p.Name != "app" {
			continue
		}
		for _, phase := range []string{"format", "lint", "build", "test"} {
			if slices.Equal(argv, p.Commands[phase].Argv) {
				return phase
			}
		}
	}
	t.Errorf("command differs from captured argv: %q", argv)
	return "unknown"
}

func TestConfiguredRegressionBuild654(t *testing.T) {
	for _, c := range conventions654() {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", c.framework, shared), func(t *testing.T) {
				f := newFixture654(t, c)
				hash := f.plan.Hash()
				a := &agent654{t: t, f: f, repair: true, tool: true}
				a.edit = func(ws string, round int) {
					for i, name := range c.added {
						if round == 2 && i == 1 {
							if err := os.Remove(filepath.Join(ws, name)); err != nil {
								t.Fatal(err)
							}
							continue
						}
						write468(t, ws, name, regressionText654(c, name, false, round))
					}
					for name := range f.baseline {
						write468(t, ws, name, "candidate replacement policy or protected source\n")
					}
					for _, name := range c.refused {
						write468(t, ws, name, regressionText654(c, name, false, round))
					}
				}
				a.inspect = func(root string, round int) {
					verifyProtected654(t, f, root)
					for i, name := range c.added {
						if round == 2 && i == 1 {
							if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
								t.Errorf("repair removal lost: %s", name)
							}
							continue
						}
						if got, want := readOptional654(root, name), regressionText654(c, name, false, round); got != want {
							t.Errorf("admitted regression %s missing or stale: %q", name, got)
						}
					}
					if c.framework == "go" {
						pkgs, err := packages(t.Context(), root, f.plan)
						if err != nil || !slices.Equal(pkgs, []string{"./app/new", "./app/page"}) {
							t.Errorf("deleted/embedded Go ownership: %v, %v", pkgs, err)
						}
					}
				}
				f.runner.hook = func(_ context.Context, q DependencyRequest, argv []string) ([]byte, error) {
					if phase654(t, f, q, argv) == "test" {
						a.inspect(q.SourceDir, a.rounds)
					}
					return nil, nil
				}
				result, err := build654(t, f, a, shared)
				if err != nil || result == nil || !result.Built() {
					t.Fatalf("build failed: %v %+v", err, result)
				}
				if a.rounds != 2 || a.reviews != 2 || len(a.toolResults) != 2 {
					t.Errorf("repair/tool/review counts: %+v", a)
				}
				for _, r := range a.toolResults {
					if !r.Passed() {
						t.Errorf("tool disagreed with final: %s", r.Summary())
					}
				}
				if len(f.runner.opens) != 4 || len(f.runner.calls) != 16 {
					t.Errorf("expected tool and fresh final commands each round: opens=%d calls=%d", len(f.runner.opens), len(f.runner.calls))
				}
				a.inspect(f.root, 2)
				for i, name := range c.added {
					if slices.Contains(result.Changes.Written, name) != (i != 1) {
						t.Errorf("wrong final writable inventory for %s: %+v", name, result.Changes)
					}
				}
				for _, name := range c.refused {
					if !slices.Contains(result.Changes.Outside, name) {
						t.Errorf("outside path unreported: %s", name)
					}
				}
				if f.plan.Hash() != hash {
					t.Fatal("build altered ratified plan")
				}
				closed484(t, f.runner.runner484)
			})
		}
	}
}

func TestConfiguredRegressionFailures654(t *testing.T) {
	for _, c := range conventions654() {
		for _, failure := range []string{"format", "test", "after tool"} {
			t.Run(c.framework+"/"+failure, func(t *testing.T) {
				f := newFixture654(t, c)
				file := c.added[0]
				a := &agent654{t: t, f: f, tool: true}
				a.edit = func(ws string, _ int) {
					text := regressionText654(c, file, failure == "test", 1)
					if failure == "format" {
						text += "\n\n\n"
					} // fixture formatter rejects excess final blank lines
					write468(t, ws, file, text)
				}
				if failure == "after tool" {
					a.afterTool = func(ws string) { write468(t, ws, file, regressionText654(c, file, true, 2)) }
				}
				sawFailure := false
				f.runner.hook = func(_ context.Context, q DependencyRequest, argv []string) ([]byte, error) {
					phase := phase654(t, f, q, argv)
					text := readOptional654(q.SourceDir, file)
					bad := phase == "format" && strings.HasSuffix(text, "\n\n\n") || phase == "test" && (text == regressionText654(c, file, true, 1) || text == regressionText654(c, file, true, 2))
					if bad {
						sawFailure = true
						return []byte("regression654 rejected by " + phase + "\n"), errors.New("exit status 1")
					}
					return nil, nil
				}
				result, err := build654(t, f, a, false)
				if err != nil || result == nil {
					t.Fatalf("fixture failed to complete: %v %+v", err, result)
				}
				if !sawFailure || result.Built() || result.Passed || a.reviews != 0 {
					t.Errorf("added %s failure escaped final checks: seen=%v reviews=%d %+v", failure, sawFailure, a.reviews, result)
				}
				if failure == "after tool" && (len(a.toolResults) != 1 || !a.toolResults[0].Passed()) {
					t.Error("post-tool case did not first pass its tool run")
				}
				if readOptional654(f.root, file) == "" {
					t.Error("failed candidate lost its admitted regression during copy-back")
				}
				closed484(t, f.runner.runner484)
			})
		}
	}
}

func TestConfiguredRegressionLinks654(t *testing.T) {
	for _, c := range conventions654()[:3] {
		for _, kind := range []string{"regular", "symbolic", "hard", "ancestor"} {
			t.Run(c.framework+"/"+kind, func(t *testing.T) {
				f := newFixture654(t, c)
				file := c.added[0]
				const marker = "host-only-regression654"
				host := t.TempDir()
				text := regressionText654(c, file, false, 1)
				if c.framework == "pytest" {
					text += "# " + marker + "\n"
				} else {
					text += "// " + marker + "\n"
				}
				write468(t, host, "secret", text)
				a := &agent654{t: t, f: f}
				a.edit = func(ws string, _ int) {
					name := filepath.Join(ws, file)
					if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
						t.Fatal(err)
					}
					switch kind {
					case "regular":
						write468(t, ws, file, text)
					case "symbolic":
						if err := os.Symlink(filepath.Join(host, "secret"), name); err != nil {
							t.Fatal(err)
						}
					case "hard":
						if err := os.Link(filepath.Join(host, "secret"), name); err != nil {
							t.Fatal(err)
						}
					case "ancestor":
						// Put the link in a new sibling test area, not over pinned files.
						file = strings.Replace(file, "/orders/", "/linked/", 1)
						write468(t, host, filepath.Base(file), text)
						if err := os.Symlink(host, filepath.Dir(filepath.Join(ws, file))); err != nil {
							t.Fatal(err)
						}
					}
				}
				f.runner.hook = func(_ context.Context, q DependencyRequest, argv []string) ([]byte, error) {
					phase654(t, f, q, argv)
					if kind != "regular" && strings.Contains(readOptional654(q.SourceDir, file), marker) {
						t.Error("check environment read a linked regression")
					}
					return nil, nil
				}
				result, err := build654(t, f, a, false)
				got := readOptional654(f.root, file)
				if kind == "regular" {
					if err != nil || result == nil || !result.Built() || got != text {
						t.Errorf("regular regression not admitted: %v %+v bytes=%q", err, result, got)
					}
				} else if strings.Contains(got, marker) {
					t.Fatal("linked host bytes copied back")
				}
				if readOptional654(host, "secret") != text {
					t.Fatal("host target changed")
				}
			})
		}
	}
}
