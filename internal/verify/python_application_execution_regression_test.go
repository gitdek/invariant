package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// This wrapper runs the embedded production runner with host paths in a
// separate request. It tests Python behavior, not Docker isolation.
func pa315LocalPython(t *testing.T, libraries string) func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		b, err := os.ReadFile(filepath.Join(c.RuntimeDir, "request.json"))
		if err != nil {
			t.Fatal(err)
		}
		var req pythonApplicationRequest
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatal(err)
		}
		req.Root, req.Output, req.Libraries = c.Root, filepath.Join(c.OutputDir, "events.jsonl"), libraries
		b, err = json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		requestFile := filepath.Join(c.OutputDir, "local-request.json")
		if err := os.WriteFile(requestFile, b, 0600); err != nil {
			t.Fatal(err)
		}
		args := append([]string(nil), c.Args...)
		args[4], args[5] = filepath.Join(c.RuntimeDir, "runner.py"), requestFile
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = filepath.Join(c.Root, strings.TrimPrefix(c.WorkingDir, "/src"))
		for k, v := range c.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		p := PythonApplicationProcess{ExitCode: -1, Stdout: stdout.String(), Stderr: stderr.String()}
		if cmd.ProcessState != nil {
			p.ExitCode = cmd.ProcessState.ExitCode()
		}
		p.Events, _ = os.ReadFile(req.Output)
		return p, err
	}
}

func TestPythonApplicationExecutionRealRunnerOutcomes(t *testing.T) {
	for _, tc := range []struct {
		method, outcome    string
		success, exercised bool
	}{
		{"test_pass", "passed", true, true},
		{"test_fail", "failed", false, true},
		{"test_error", "error", false, true},
		{"test_skip", "skipped", false, false},
		{"test_expected", "expected_failure", false, true},
		{"test_unexpected", "unexpected_success", false, true},
		{"test_subtest", "failed", false, true},
	} {
		t.Run(tc.method, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			pa315Select(t, p, "TaskTests."+tc.method, "tests.test_tasks.TaskTests."+tc.method)
			before := pa315Tree(t, root)
			n := 0
			opts := pa315Options(&n)
			opts.Execute = pa315LocalPython(t, t.TempDir())
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if r == nil || r.Success != tc.success || len(r.Tests) != 1 {
				t.Fatalf("outcome: %+v %v", r, err)
			}
			row := r.Tests[0]
			if row.Outcome != tc.outcome || row.Exercised != tc.exercised || row.Check != pa315Unit(t, s).Key.Check {
				t.Fatalf("observation: %+v %v", row, err)
			}
			if tc.method == "test_skip" && !reflect.DeepEqual(row.Diagnostics, []string{"not exercised"}) {
				t.Fatalf("skip reason lost: %+v", row)
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("runner changed originals")
			}
		})
	}
}

func TestPythonApplicationExecutionRealRunnerIdentityAndCompletion(t *testing.T) {
	for _, which := range []string{"replacement", "alias", "setup", "teardown", "class fixture", "module fixture", "partial", "stdout"} {
		t.Run(which, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			source := "import unittest, os\nclass TaskTests(unittest.TestCase):\n    def test_pass(self):\n        pass\n"
			switch which {
			case "replacement":
				source += "def replacement(self): pass\nTaskTests.test_pass = replacement\n"
			case "alias":
				source += "class Other(unittest.TestCase):\n    def test_pass(self): pass\nTaskTests.test_pass = Other.test_pass\n"
			case "setup":
				source += "    def setUp(self): raise RuntimeError('setup failed')\n"
			case "teardown":
				source += "    def tearDown(self): raise RuntimeError('teardown failed')\n"
			case "class fixture":
				source += "    @classmethod\n    def setUpClass(cls): raise RuntimeError('class failed')\n"
			case "module fixture":
				source += "def setUpModule(): raise RuntimeError('module failed')\n"
			case "partial":
				source += "    def test_z(self): os._exit(0)\n"
			case "stdout":
				source += "    def test_z(self): print('{\"type\":\"end\",\"count\":99}')\n"
			}
			pa310Write(t, root, pa315TestSource, source)
			pa315Select(t, p, "TaskTests.test_pass", "tests.test_tasks")
			opts := pa315Options(new(int))
			opts.Execute = pa315LocalPython(t, t.TempDir())
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if r == nil || r.Success != (which == "stdout") {
				t.Fatalf("wrong credit: %+v %v", r, err)
			}
			if which == "partial" && (r.Complete || len(r.Tests) != 1 || r.Tests[0].Outcome != "passed") {
				t.Fatalf("partial evidence lost: %+v", r)
			}
		})
	}
}

func TestPythonApplicationExecutionRealRunnerStartup(t *testing.T) {
	root, p := pa315Fixture(t, false)
	libraries := t.TempDir()
	for name, body := range map[string]string{
		"core/__init__.py": "ORIGIN='dependency'\n", "core/nested/__init__.py": "ORIGIN='dependency'\n",
		"core/nested/logic.py": "ORIGIN='dependency'\n", "driver_support.py": "ORIGIN='dependency'\n",
		"sitecustomize.py": "raise RuntimeError('site hook')\n", "usercustomize.py": "raise RuntimeError('user hook')\n",
		"hostile.pth": "import os; os._exit(91)\n",
	} {
		pa310Write(t, libraries, name, body)
	}
	pa310Write(t, root, "core/nested/logic.py", "ORIGIN='application'\n")
	pa310Write(t, root, pa315Project+"/driver_support.py", "ORIGIN='driver'\n")
	for _, name := range []string{"unittest.py", "json.py", "sitecustomize.py", "usercustomize.py"} {
		pa310Write(t, root, name, "raise RuntimeError('application shadow')\n")
	}
	pa310Write(t, root, pa315TestSource, `import os, sys, unittest
class TaskTests(unittest.TestCase):
    def test_pass(self):
        import core.nested.logic, driver_support
        self.assertEqual(core.nested.logic.ORIGIN, 'application')
        self.assertEqual(driver_support.ORIGIN, 'driver')
        self.assertEqual(sys.flags.no_site, 1)
        self.assertEqual(sys.flags.no_user_site, 1)
        self.assertTrue(sys.flags.safe_path)
        self.assertNotIn('', sys.path)
        self.assertNotIn('sitecustomize', sys.modules)
        self.assertNotIn('usercustomize', sys.modules)
        self.assertNotIn('PA315_HOST_SECRET', os.environ)
        self.assertEqual(os.environ['PYTHONHASHSEED'], '0')
`)
	// Path order alone loses the namespace to the installed regular package.
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	control := exec.Command(python, "-I", "-S", "-c", "import sys; sys.path.extend(sys.argv[1:]); import core.nested.logic; assert core.nested.logic.ORIGIN == 'dependency'", root, libraries)
	if b, err := control.CombinedOutput(); err != nil {
		t.Fatalf("inactive hostile fixture: %v %s", err, b)
	}
	t.Setenv("PA315_HOST_SECRET", "secret")
	opts := pa315Options(new(int))
	opts.Execute = pa315LocalPython(t, libraries)
	s := pa315Prepare(t, p, opts)
	r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
	if err != nil || r == nil || !r.Success {
		t.Fatalf("controlled startup: %+v %v", r, err)
	}
}

func TestPythonApplicationExecutionSnapshotDuringOperations(t *testing.T) {
	for _, during := range []string{"prepare", "run"} {
		t.Run(during, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			before := pa315Tree(t, root)
			opts := pa315Options(new(int))
			if during == "prepare" {
				opts.SelectImage = func(_ context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
					pa310Write(t, w.Root, "resources/changed", "new")
					return toolchain.PythonImage, nil, nil
				}
			} else {
				opts.Execute = func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					pa310Write(t, c.Root, "resources/changed", "new")
					return PythonApplicationProcess{Events: pa315Stream(t, c, pa315Event(c, before[pa315TestSource], "passed"), map[string]any{"type": "end", "count": 1})}, nil
				}
			}
			s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), opts)
			if during == "prepare" {
				if err == nil || s != nil {
					t.Fatal("preparation mutation accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
				if err == nil || r == nil || r.Success || r.Complete || len(r.Tests) != 1 {
					t.Fatalf("mutation evidence: %+v %v", r, err)
				}
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("originals changed")
			}
		})
	}
}

func TestPythonApplicationExecutionAdditionalProtocolFailures(t *testing.T) {
	for _, which := range []string{"duplicate field", "truncated end", "trailing row", "null diagnostic", "missing exercised", "wrong source", "foreign check", "contradictory skip"} {
		t.Run(which, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			hash := pa315Tree(t, root)[pa315TestSource]
			opts := pa315Options(new(int))
			opts.Execute = func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				first := pa315Event(c, hash, "passed")
				bad := pa315Event(c, hash, "passed")
				bad["id"] = "second"
				switch which {
				case "null diagnostic":
					bad["diagnostics"] = []any{nil}
				case "missing exercised":
					delete(bad, "exercised")
				case "wrong source":
					bad["source_sha256"] = "sha256:" + strings.Repeat("e", 64)
				case "foreign check":
					bad["check"] = "foreign"
				case "contradictory skip":
					bad["outcome"] = "skipped"
				}
				b := pa315Stream(t, c, first, bad, map[string]any{"type": "end", "count": 2})
				switch which {
				case "duplicate field":
					b = bytes.Replace(b, []byte(`"id":"second"`), []byte(`"id":"second","id":"third"`), 1)
				case "truncated end":
					b = b[:len(b)-1]
				case "trailing row":
					b = append(b, []byte("{}\n")...)
				}
				return PythonApplicationProcess{Events: b}, nil
			}
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if err == nil || r == nil || r.Success || r.Complete || len(r.Tests) == 0 || r.Tests[0].Outcome != "passed" {
				t.Fatalf("invalid output granted credit or lost prefix: %+v %v", r, err)
			}
		})
	}
}

func TestPythonApplicationExecutionDockerCommandLimits(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "argv")
	pa310Write(t, bin, "docker", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PA315_ARGS\"\nexit 125\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PA315_ARGS", log)
	_, p := pa315Fixture(t, false)
	opts := pa315Options(new(int))
	opts.Execute = RunPythonApplicationCommand
	s := pa315Prepare(t, p, opts)
	r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
	if err == nil || r.Success {
		t.Fatalf("failed image succeeded: %+v %v", r, err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--pull=never\n", "--network\nnone\n", "--platform\nlinux/amd64\n", "--memory\n512m\n", "--pids-limit\n128\n", "--read-only\n", ":/src:ro\n", ":/runtime:ro\n", ":/output:rw\n", "--entrypoint\n/usr/bin/env\n", "-i\n", "PYTHONHASHSEED=0\n"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing isolation %q in %s", want, b)
		}
	}
}
