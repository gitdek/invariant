package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Run with INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1. This uses the same
// session and executor as callers, actual pinned Python, and actual PyPI wheel
// resolution through SelectImage. No fake executable or simulated Python result
// is used. Without the opt-in, Docker-free contract tests still run in the other
// acceptance file. Additional real regression tests required by that contract
// use this same prefix and opt-in, and extend this reusable lifecycle fixture.
func TestPythonApplicationExecutionReal(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
		t.Skip("real Docker validation is merger work; set INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if b, err := toolchain.Docker(ctx, "info").CombinedOutput(); err != nil {
		t.Fatalf("real suite was requested but Docker is unavailable: %v\n%s", err, b)
	}
	for _, wheel := range []bool{false, true} {
		name := "stdlib"
		if wheel {
			name = "wheel"
		}
		t.Run(name, func(t *testing.T) {
			root, p := pa315Fixture(t, wheel)
			before := pa315Tree(t, root)
			executions := []PythonApplicationCommand{}
			opts := PythonApplicationSessionOptions{Execute: func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				executions = append(executions, c)
				return RunPythonApplicationCommand(ctx, c)
			}}
			s := pa315Prepare(t, p, opts)
			v := pa315Unit(t, s)
			first, err := s.Run(ctx, v.Key)
			if err != nil || first == nil || !first.Success || !first.Complete {
				t.Fatalf("real lifecycle test: %+v %v", first, err)
			}
			if len(first.Tests) != 1 || first.Tests[0].Check != v.Key.Check || !first.Tests[0].Exercised || first.Tests[0].Outcome != "passed" {
				t.Fatalf("no actual per-test identity: %+v", first)
			}
			if !reflect.DeepEqual(pa315Files(first.Provenance), before) {
				t.Fatal("result omits actual staged inputs")
			}
			if wheel {
				deps := first.Provenance.Dependencies
				if deps == nil || deps.Base != toolchain.PythonImage || deps.Platform != "linux/amd64" || deps.Recipe == "" || len(deps.Inputs) != 3 || len(deps.CanonicalInputs) != 2 || len(deps.Artifacts) != 1 {
					t.Fatalf("incomplete dependencies: %+v", deps)
				}
				a := deps.Artifacts[0]
				if a.Name != "packaging" || a.Version != "24.2" || a.Filename != "packaging-24.2-py3-none-any.whl" || !pyDepsDigest.MatchString(a.SHA256) {
					t.Fatalf("wrong installed wheel: %+v", a)
				}
				for _, in := range deps.Inputs {
					if before[in.Path] != in.SHA256 {
						t.Fatalf("wrong root dependency hash: %+v", in)
					}
				}
			} else if first.Provenance.Dependencies != nil {
				t.Fatal("invented standard-library dependencies")
			}
			second, err := s.Run(ctx, v.Key)
			if err != nil || second == nil || !second.Success || first.Fingerprint != second.Fingerprint {
				t.Fatalf("repeat differs: %+v %v", second, err)
			}
			if len(executions) != 2 || executions[0].Image == "" || executions[0].Image != executions[1].Image {
				t.Fatal("selected image not reused")
			}
			// A separately staged and relocated identical application has identical
			// portable evidence, despite host paths and operational image metadata.
			relocatedRoot, q := pa315Fixture(t, wheel)
			relocated := pa315Prepare(t, q, PythonApplicationSessionOptions{})
			third, err := relocated.Run(ctx, pa315Unit(t, relocated).Key)
			if err != nil || third == nil || !third.Success || first.Fingerprint != third.Fingerprint {
				t.Fatalf("relocation differs: %+v %v", third, err)
			}
			a, _ := json.Marshal(first)
			b, _ := json.Marshal(second)
			c, _ := json.Marshal(third)
			if string(a) != string(b) || string(a) != string(c) {
				t.Fatalf("portable serialization differs on repeat/relocation:\n%s\n%s\n%s", a, b, c)
			}
			for _, host := range []string{root, relocatedRoot, executions[0].Root, executions[0].RuntimeDir, executions[0].OutputDir} {
				if host != "" && strings.Contains(string(a), host) {
					t.Fatalf("portable result leaks host path %q", host)
				}
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("real execution changed original")
			}
		})
	}
	t.Run("hostile installed packages and startup", func(t *testing.T) {
		pa315RealHostileLibraries(t, ctx)
	})
	t.Run("module selections and outcomes", func(t *testing.T) {
		cases := []struct {
			symbol, selection, outcome string
			success                    bool
		}{
			{"TaskTests.test_pass", "tests.test_tasks.TaskTests.test_pass", "passed", true},
			{"TaskTests.test_fail", "tests.test_tasks.TaskTests.test_fail", "failed", false},
			{"TaskTests.test_error", "tests.test_tasks.TaskTests.test_error", "error", false},
			{"TaskTests.test_skip", "tests.test_tasks.TaskTests.test_skip", "skipped", false},
			{"TaskTests.test_expected", "tests.test_tasks.TaskTests.test_expected", "expected_failure", false},
			{"TaskTests.test_unexpected", "tests.test_tasks.TaskTests.test_unexpected", "unexpected_success", false},
			{"TaskTests.test_subtest", "tests.test_tasks.TaskTests.test_subtest", "failed", false},
			{"TaskTests.test_fail", "tests.test_tasks.TaskTests.test_pass", "not_run", false},
			{"TaskTests.test_pass", "missing_test_module", "not_run", false},
		}
		for _, tc := range cases {
			t.Run(tc.symbol+"/"+tc.selection, func(t *testing.T) {
				root, p := pa315Fixture(t, false)
				pa315Select(t, p, tc.symbol, tc.selection)
				before := pa315Tree(t, root)
				s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
				v := pa315Unit(t, s)
				r, err := s.Run(ctx, v.Key)
				if r == nil || r.Success != tc.success || (tc.success && (err != nil || !r.Complete)) {
					t.Fatalf("wrong real outcome: %+v %v", r, err)
				}
				found := false
				for _, row := range r.Tests {
					if row.Check == v.Key.Check {
						found = true
						if row.Outcome != tc.outcome {
							t.Fatalf("got %s, want %s", row.Outcome, tc.outcome)
						}
						if tc.outcome == "not_run" && row.Exercised {
							t.Fatal("unexecuted required test credited")
						}
						if (tc.outcome == "failed" || tc.outcome == "error" || tc.outcome == "skipped") && len(row.Diagnostics) == 0 {
							t.Fatal("lost test diagnostic")
						}
					}
				}
				if !found {
					t.Fatalf("required check's outcome missing: %+v", r.Tests)
				}
				if !reflect.DeepEqual(before, pa315Tree(t, root)) {
					t.Fatal("failure modified originals")
				}
			})
		}
		// Module selection must enumerate the actual loaded suite, not assume
		// that a selected module exercised the configured declaration.
		root, p := pa315Fixture(t, false)
		pa310Write(t, root, pa315TestSource, "import unittest\nfrom tasks.lifecycle import advance\nclass TaskTests(unittest.TestCase):\n    def test_pass(self):\n        self.assertEqual(advance('pending'),'running')\n    def test_fail(self):\n        self.fail('module failure')\n")
		pa315Select(t, p, "TaskTests.test_pass", "tests.test_tasks")
		s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
		r, err := s.Run(ctx, pa315Unit(t, s).Key)
		if r == nil || r.Success || len(r.Tests) < 2 {
			t.Fatalf("module failure/outcomes lost: %+v %v", r, err)
		}
		seen := map[string]bool{}
		for _, row := range r.Tests {
			seen[row.Outcome] = true
		}
		if !seen["passed"] || !seen["failed"] {
			t.Fatalf("module did not run both tests: %+v", r.Tests)
		}
	})
	t.Run("partial completion and substituted method", func(t *testing.T) {
		_, p := pa315Fixture(t, false)
		pa315Select(t, p, "TaskTests.test_partial_a", "tests.test_tasks.TaskTests.test_partial_a", "tests.test_tasks.TaskTests.test_partial_z")
		s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
		r, err := s.Run(ctx, pa315Unit(t, s).Key)
		if r == nil || r.Complete || r.Success || len(r.Tests) == 0 || r.Tests[0].Outcome != "passed" {
			t.Fatalf("os._exit(0) concealed partial evidence: %+v %v", r, err)
		}
		root, q := pa315Fixture(t, false)
		f := filepath.Join(root, pa315TestSource)
		b, e := os.ReadFile(f)
		if e != nil {
			t.Fatal(e)
		}
		pa310Write(t, root, pa315TestSource, string(b)+"\ndef substitute(self):\n    pass\nTaskTests.test_pass = substitute\n")
		sub := pa315Prepare(t, q, PythonApplicationSessionOptions{})
		result, e := sub.Run(ctx, pa315Unit(t, sub).Key)
		if result != nil && result.Success {
			t.Fatalf("same name credited different lexical code: %+v %v", result, e)
		}
	})
	t.Run("working directory literal environment and controlled startup", func(t *testing.T) {
		root, p := pa315Fixture(t, false)
		literal, _ := json.Marshal(pa315Literal)
		code := `import os, sys, socket, unittest
from pathlib import Path
from tasks.lifecycle import advance
class TaskTests(unittest.TestCase):
    def test_pass(self):
        self.assertEqual(advance('pending'), 'running')
        self.assertEqual(os.getcwd(), '/src/verification/lifecycle')
        self.assertEqual(Path('cwd.txt').read_text(), 'declared cwd')
        self.assertEqual(os.environ['APP_LITERAL'], EXPECTED_LITERAL)
        self.assertEqual(os.environ['APP_EMPTY'], '')
        self.assertNotIn('PA315_HOST_SECRET', os.environ)
        for key, value in os.environ.items():
            if key.lower().endswith('_proxy'):
                self.assertEqual(value, '')
        self.assertEqual(os.environ['PYTHONHASHSEED'], '0')
        self.assertEqual(sys.flags.no_site, 1)
        self.assertEqual(sys.flags.no_user_site, 1)
        self.assertTrue(sys.flags.safe_path)
        self.assertEqual(hash('invariant'), int(os.environ['APP_HASH']))
        self.assertNotIn('sitecustomize', sys.modules)
        self.assertNotIn('usercustomize', sys.modules)
        self.assertNotIn('', sys.path)
        self.assertEqual(Path(sys.modules['unittest'].__file__).parts[1], 'usr')
        with self.assertRaises(OSError):
            socket.create_connection(('1.1.1.1', 443), timeout=0.3)
`
		pa310Write(t, root, pa315TestSource, strings.Replace(code, "EXPECTED_LITERAL", string(literal), 1))
		pa310Write(t, root, pa315Project+"/cwd.txt", "declared cwd")
		for _, f := range []string{"sitecustomize.py", "usercustomize.py", "unittest.py", "json.py"} {
			pa310Write(t, root, f, "raise RuntimeError('application shadow took over trusted startup')\n")
		}
		// Obtain a real independent seed-0 reference from the exact pinned
		// interpreter. -I would ignore the seed, so it is intentionally absent.
		seed, err := toolchain.Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "--env", "PYTHONHASHSEED=0", toolchain.PythonImage, "/usr/local/bin/python", "-S", "-s", "-P", "-c", "print(hash('invariant'))").CombinedOutput()
		if err != nil {
			t.Fatalf("hash seed reference: %v %s", err, seed)
		}
		p.Manifest.Components.Checks[0].Invocations[0].Env["APP_HASH"] = strings.TrimSpace(string(seed))
		p.Manifest.Components.Checks[0].Invocations[0].WorkingDir = pa315Project
		pa310Save(t, p)
		t.Setenv("PA315_HOST_SECRET", "must not enter container")
		t.Setenv("HTTP_PROXY", "http://credentials.invalid:1234")
		t.Setenv("https_proxy", "http://credentials.invalid:1234")
		before := pa315Tree(t, root)
		s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
		r, err := s.Run(ctx, pa315Unit(t, s).Key)
		if err != nil || r == nil || !r.Success {
			t.Fatalf("controlled real startup failed: %+v %v", r, err)
		}
		if r.Declared.WorkingDir != pa315Project || r.Actual.WorkingDir != "/src/"+pa315Project || r.Declared.Command != "python" || r.Actual.Interpreter != "/usr/local/bin/python" || r.Actual.Env["APP_LITERAL"] != pa315Literal {
			t.Fatalf("declared/actual conflated: %+v", r)
		}
		if !reflect.DeepEqual(before, pa315Tree(t, root)) {
			t.Fatal("startup run changed original")
		}
	})
	t.Run("snapshot mutation during execution", func(t *testing.T) {
		root, p := pa315Fixture(t, false)
		before := pa315Tree(t, root)
		opts := PythonApplicationSessionOptions{Execute: func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			r, err := RunPythonApplicationCommand(ctx, c)
			pa310Write(t, c.Root, "siblings/unselected.py", "VALUE = 999\n")
			return r, err
		}}
		s := pa315Prepare(t, p, opts)
		r, err := s.Run(ctx, pa315Unit(t, s).Key)
		if err == nil || (r != nil && r.Success) {
			t.Fatalf("mutation kept old success: %+v %v", r, err)
		}
		if !reflect.DeepEqual(before, pa315Tree(t, root)) {
			t.Fatal("mutation escaped staged copy")
		}
	})
	t.Run("cancellation removes container", func(t *testing.T) {
		root, p := pa315Fixture(t, false)
		pa315Select(t, p, "TaskTests.test_wait", "tests.test_tasks.TaskTests.test_wait")
		before := pa315Tree(t, root)
		containers := func() map[string]bool {
			b, err := toolchain.Docker(ctx, "ps", "-aq", "--filter", "name=invariant-").CombinedOutput()
			if err != nil {
				t.Fatalf("list containers: %v %s", err, b)
			}
			m := map[string]bool{}
			for _, id := range strings.Fields(string(b)) {
				m[id] = true
			}
			return m
		}
		s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
		old := containers()
		runCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		r, err := s.Run(runCtx, pa315Unit(t, s).Key)
		if err == nil || !errors.Is(runCtx.Err(), context.DeadlineExceeded) || (r != nil && r.Success) {
			t.Fatalf("cancelled execution succeeded: %+v %v", r, err)
		}
		for id := range containers() {
			if !old[id] {
				t.Fatalf("cancelled container remains: %s", id)
			}
		}
		if !reflect.DeepEqual(before, pa315Tree(t, root)) {
			t.Fatal("cancelled execution modified originals")
		}
	})
	t.Run("missing pinned image cannot fall back", func(t *testing.T) {
		// No destructive daemon image removal: a unique absent immutable ID
		// requests exactly the same missing-image behavior from the executor.
		root, p := pa315Fixture(t, false)
		before := pa315Tree(t, root)
		calls := 0
		opts := PythonApplicationSessionOptions{Execute: func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			calls++
			c.Image = "sha256:" + strings.Repeat("0", 64)
			return RunPythonApplicationCommand(ctx, c)
		}}
		s := pa315Prepare(t, p, opts)
		r, err := s.Run(ctx, pa315Unit(t, s).Key)
		if err == nil || calls != 1 || (r != nil && r.Success) {
			t.Fatalf("missing image fell back: %+v %v calls=%d", r, err, calls)
		}
		if !reflect.DeepEqual(before, pa315Tree(t, root)) {
			t.Fatal("image failure modified original")
		}
	})
}

func pa315RealHostileLibraries(t *testing.T, ctx context.Context) {
	t.Helper()
	// Capture the existing builder's recipe for fixture setup. This double
	// never supplies execution outcomes: every probe below runs real Python.
	var recipe string
	t.Run("fixture recipe", func(t *testing.T) {
		pyDepsFakeDocker(t)
		root, _ := pa315Fixture(t, true)
		pyDepsBuild(t, root)
		recipe = pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context"))["Dockerfile"]
		if recipe == "" {
			t.Fatal("dependency recipe missing")
		}
	})
	for _, vector := range []string{"imports", "python", "pth", "sitecustomize", "usercustomize"} {
		t.Run(vector, func(t *testing.T) {
			root, p := pa315Fixture(t, true)
			var wheel, hash, distribution string
			if vector == "imports" {
				wheel, hash = pyDepsImportOrderWheel(t, true)
				distribution = "invariant-application-shadow"
				for name, origin := range map[string]string{
					"application/__init__.py":                          "application",
					"core/core.py":                                     "root namespace",
					"core/nested/logic.py":                             "nested namespace",
					pa315Project + "/driver_support.py":                "driver",
					pa315Project + "/driver_namespace/logic.py":        "driver namespace",
					pa315Project + "/driver_namespace/nested/logic.py": "driver nested namespace",
				} {
					pa310Write(t, root, name, "ORIGIN = '"+origin+"'\n")
				}
				pa310Write(t, root, pa315TestSource, `import unittest
from tasks.lifecycle import advance
class TaskTests(unittest.TestCase):
    def test_pass(self):
        import application, core.core, core.nested.logic, driver_support
        import driver_namespace.logic, driver_namespace.nested.logic, fractions
        self.assertEqual(advance('pending'), 'running')
        self.assertEqual(application.ORIGIN, 'application')
        self.assertEqual(core.core.ORIGIN, 'root namespace')
        self.assertEqual(core.nested.logic.ORIGIN, 'nested namespace')
        self.assertEqual(driver_support.ORIGIN, 'driver')
        self.assertEqual(driver_namespace.logic.ORIGIN, 'driver namespace')
        self.assertEqual(driver_namespace.nested.logic.ORIGIN, 'driver nested namespace')
        self.assertTrue(fractions.__file__.startswith('/usr/local/lib/python'))
`)
			} else {
				wheel, hash = pyDepsShadowWheel(t, vector)
				distribution = "invariant-gate-shadow"
				pa310Write(t, root, pa315TestSource, `import unittest
from tasks.lifecycle import advance
class TaskTests(unittest.TestCase):
    def test_pass(self):
        import builtins
        self.assertFalse(hasattr(builtins, 'INVARIANT_IMPORTED_LIBRARY'))
        import invariant_gate_shadow
        self.assertEqual(builtins.INVARIANT_IMPORTED_LIBRARY, 'import-only')
        self.assertEqual(advance('pending'), 'running')
`)
			}
			before := pa315Tree(t, root)
			docker := func(args ...string) []byte {
				t.Helper()
				b, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
				if err != nil {
					t.Fatalf("hostile fixture Docker: %v\n%s", err, b)
				}
				return bytes.TrimSpace(b)
			}
			count := 0
			opts := PythonApplicationSessionOptions{SelectImage: func(ctx context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				count++
				_, first, err := w.SelectImage(ctx)
				if err != nil {
					return "", nil, err
				}
				if first == nil {
					t.Fatal("missing real wheel environment")
				}
				selected := pyDepsInstallImportWheel(t, ctx, docker, w.Root, recipe, first, wheel, hash, distribution)
				target := pyDepsRecipeLibraryTarget(t, recipe)
				if vector == "imports" {
					// Even application paths first lose namespaces to regular
					// dependency packages without the production import policy.
					control := `import sys
sys.path.extend(['/src', '/src/verification/lifecycle', sys.argv[1]])
import core.core, core.nested.logic, driver_namespace.logic, driver_namespace.nested.logic
assert all(m.ORIGIN == 'dependency' for m in [core.core,core.nested.logic,driver_namespace.logic,driver_namespace.nested.logic])
sys.path.insert(0,sys.argv[1])
import application, driver_support
assert application.ORIGIN == driver_support.ORIGIN == 'dependency'
print('hostile fixture active')
`
					out := docker("run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", "-w", "/tmp", "-v", w.Root+":/src:ro", selected.Image, "/usr/local/bin/python", "-I", "-c", control, target)
					if string(out) != "hostile fixture active" {
						t.Fatalf("negative control did not exercise imports: %s", out)
					}
				} else {
					pyDepsShadowNegativeControl(t, ctx, docker, selected.Image, target, pyDepsRecipeReportPath(t, recipe), vector)
				}
				return selected.Image, selected, nil
			}}
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(ctx, pa315Unit(t, s).Key)
			if err != nil || r == nil || !r.Success || count != 1 {
				t.Fatalf("hostile %s changed startup or imports: %+v %v", vector, r, err)
			}
			if len(r.Provenance.Dependencies.Artifacts) != 2 {
				t.Fatal("lost actual hostile-wheel fixture provenance")
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("hostile fixture changed original inputs")
			}
		})
	}
}
