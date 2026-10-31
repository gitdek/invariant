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
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// This executes the embedded runner with real local Python, translating only
// container mount paths. Docker isolation is covered separately by the opt-in
// real suite; no orchestration double manufactures these test observations.
func pa315LocalPython(t *testing.T) PythonApplicationSessionOptions {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	o := pa315Options(new(int))
	o.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		runner := strings.NewReplacer("/src", filepath.ToSlash(c.Root), "/runtime", filepath.ToSlash(c.RuntimeDir), "/output", filepath.ToSlash(c.OutputDir)).Replace(string(pythonApplicationRunner))
		args := append([]string{"-S", "-s", "-P", "-B", "-c", runner, filepath.Join(c.RuntimeDir, "request.json")}, c.Args[6:]...)
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = filepath.Join(c.Root, strings.TrimPrefix(c.WorkingDir, "/src"))
		for k, v := range c.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		p := PythonApplicationProcess{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1}
		if cmd.ProcessState != nil {
			p.ExitCode = cmd.ProcessState.ExitCode()
		}
		p.Events, _ = os.ReadFile(filepath.Join(c.OutputDir, "events.jsonl"))
		return p, err
	}
	return o
}

func TestPythonApplicationExecutionRunnerRegression(t *testing.T) {
	for _, tc := range []struct {
		method, outcome string
		success         bool
	}{
		{"test_pass", "passed", true}, {"test_fail", "failed", false},
		{"test_error", "error", false}, {"test_skip", "skipped", false},
		{"test_expected", "expected_failure", false}, {"test_unexpected", "unexpected_success", false},
		{"test_subtest", "failed", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			_, p := pa315Fixture(t, false)
			pa315Select(t, p, "TaskTests."+tc.method, "tests.test_tasks.TaskTests."+tc.method)
			s := pa315Prepare(t, p, pa315LocalPython(t))
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if r == nil || r.Success != tc.success || len(r.Tests) != 1 || r.Tests[0].Outcome != tc.outcome || (tc.success && err != nil) {
				t.Fatalf("actual Python outcome: %+v %v", r, err)
			}
		})
	}
	for _, tc := range []struct {
		name, code         string
		success, exercised bool
		outcome            string
	}{
		{"setup", "    def setUp(self):\n        raise RuntimeError('setup failed')\n", false, false, "error"},
		{"teardown", "    def tearDown(self):\n        raise RuntimeError('teardown failed')\n", false, true, "error"},
		{"class fixture", "    @classmethod\n    def setUpClass(cls):\n        raise RuntimeError('fixture failed')\n", false, false, "not_run"},
		{"overridden runtime ID", "    def id(self):\n        return 'different.display.name'\n", true, true, "passed"},
		{"alias", "def other(self):\n    pass\nTaskTests.test_pass = other\n", false, false, "not_run"},
		{"same named replacement", "class Replacement:\n    def test_pass(self):\n        pass\nTaskTests.test_pass = Replacement.test_pass\n", false, false, "not_run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			pa310Write(t, root, pa315TestSource, "import unittest\nclass TaskTests(unittest.TestCase):\n    def test_pass(self):\n        pass\n"+tc.code)
			s := pa315Prepare(t, p, pa315LocalPython(t))
			key := pa315Unit(t, s).Key
			r, err := s.Run(context.Background(), key)
			if r == nil || r.Success != tc.success {
				t.Fatalf("%+v %v", r, err)
			}
			found := false
			for _, row := range r.Tests {
				if row.Check == key.Check {
					found = true
					if row.Outcome != tc.outcome || row.Exercised != tc.exercised {
						t.Fatalf("wrong method evidence: %+v", row)
					}
				}
			}
			if !found {
				t.Fatal("missing required observation")
			}
		})
	}
	t.Run("module and partial completion", func(t *testing.T) {
		_, p := pa315Fixture(t, false)
		pa315Select(t, p, "TaskTests.test_partial_a", "tests.test_tasks.TaskTests.test_partial_a", "tests.test_tasks.TaskTests.test_partial_z")
		s := pa315Prepare(t, p, pa315LocalPython(t))
		r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
		if err == nil || r == nil || r.Complete || r.Success || len(r.Tests) != 1 || r.Tests[0].Outcome != "passed" {
			t.Fatalf("partial outcome lost: %+v %v", r, err)
		}
		root, q := pa315Fixture(t, false)
		pa310Write(t, root, pa315TestSource, "import unittest\nclass TaskTests(unittest.TestCase):\n    def test_pass(self):\n        pass\n    def test_fail(self):\n        self.fail('module failure')\n")
		pa315Select(t, q, "TaskTests.test_pass", "tests.test_tasks")
		s = pa315Prepare(t, q, pa315LocalPython(t))
		r, _ = s.Run(context.Background(), pa315Unit(t, s).Key)
		if r == nil || r.Success || len(r.Tests) != 2 {
			t.Fatalf("module not enumerated: %+v", r)
		}
	})
}

func TestPythonApplicationExecutionProtocolRegression(t *testing.T) {
	for _, suffix := range []string{"{bad}\n", "null\n", "{\"type\":\"end\",\"count\":1,\"count\":1}\n", "{\"type\":\"end\",\"count\":1}", "\xff\n"} {
		t.Run(suffix, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			o := pa315Options(new(int))
			o.Execute = func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				b := pa315Stream(t, c, pa315Event(c, pa315Tree(t, root)[pa315TestSource], "passed"))
				return PythonApplicationProcess{Events: append(b, []byte(suffix)...)}, nil
			}
			s := pa315Prepare(t, p, o)
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if err == nil || r == nil || r.Complete || r.Success || len(r.Tests) != 1 || r.Tests[0].Outcome != "passed" {
				t.Fatalf("valid prefix lost: %+v %v", r, err)
			}
		})
	}
	t.Run("mutation during selection and execution", func(t *testing.T) {
		for _, preparation := range []bool{true, false} {
			root, p := pa315Fixture(t, false)
			before := pa315Tree(t, root)
			o := pa315Options(new(int))
			selectImage := o.SelectImage
			o.SelectImage = func(ctx context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				if preparation {
					pa310Write(t, w.Root, "siblings/unselected.py", "changed\n")
				}
				return selectImage(ctx, w)
			}
			o.Execute = func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				b := pa315Stream(t, c, pa315Event(c, before[pa315TestSource], "passed"), map[string]any{"type": "end", "count": 1})
				pa310Write(t, c.Root, "siblings/unselected.py", "changed\n")
				return PythonApplicationProcess{Events: b}, nil
			}
			s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), o)
			if preparation {
				if s != nil || err == nil {
					t.Fatal("selection mutation accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
				if err == nil || r == nil || r.Success || r.Complete || len(r.Tests) != 1 {
					t.Fatalf("execution mutation accepted: %+v %v", r, err)
				}
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("original changed")
			}
		}
	})
}

func TestPythonApplicationExecutionRealLimits(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
		t.Skip("real Docker validation is merger work")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root, p := pa315Fixture(t, false)
	pa310Write(t, root, pa315TestSource, `import unittest
from pathlib import Path
class TaskTests(unittest.TestCase):
    def test_pass(self):
        def limit(modern, legacy):
            p = Path('/sys/fs/cgroup') / modern
            if not p.exists():
                p = Path('/sys/fs/cgroup') / legacy
            return int(p.read_text())
        self.assertEqual(limit('memory.max', 'memory/memory.limit_in_bytes'), 512 * 1024 * 1024)
        self.assertEqual(limit('pids.max', 'pids/pids.max'), 128)
        for name in ['/src/new.txt', '/runtime/new.txt', '/root/new.txt']:
            with self.assertRaises(OSError):
                Path(name).write_text('must be read only')
        Path('/output/generated.txt').write_text('separate output')
`)
	before := pa315Tree(t, root)
	s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
	r, err := s.Run(ctx, pa315Unit(t, s).Key)
	if err != nil || r == nil || !r.Success {
		t.Fatalf("real resource limits: %+v %v", r, err)
	}
	b, _ := json.Marshal(r.Provenance)
	if strings.Contains(string(b), "generated.txt") || !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("outputs entered input provenance")
	}
}

func TestPythonApplicationExecutionDependencyRegression(t *testing.T) {
	for _, mutation := range []string{"none", "missing include", "extra input", "wrong input hash", "missing canonical", "bad canonical hash", "bad recipe", "bad platform", "bad base", "bad wheel", "bad version", "bad wheel hash"} {
		t.Run(mutation, func(t *testing.T) {
			noTools := pa315TrapTools(t)
			root, p := pa315Fixture(t, true)
			files := pa315Tree(t, root)
			// The selector double supplies preparation evidence only, never
			// outcomes. It must not need an additional Docker preparation.
			d := &toolchain.PythonDependencies{
				Image: pa315Image, Base: toolchain.PythonImage, Platform: "linux/amd64", Recipe: pa315Image,
				Inputs:          []toolchain.PythonInput{{Path: "requirements.txt", SHA256: files["requirements.txt"]}, {Path: "deps/application.txt", SHA256: files["deps/application.txt"]}, {Path: "deps/constraints.txt", SHA256: files["deps/constraints.txt"]}},
				CanonicalInputs: []toolchain.PythonInput{{Path: "requirements.txt", SHA256: pa310Hash([]byte("packaging<25,>=24\n"))}, {Path: "constraints.txt", SHA256: pa310Hash([]byte("packaging==24.2\n"))}},
				Artifacts:       []toolchain.PythonArtifact{{Name: "packaging", Version: "24.2", Filename: "packaging-24.2-py3-none-any.whl", SHA256: pa315Image}},
			}
			switch mutation {
			case "missing include":
				d.Inputs = d.Inputs[:2]
			case "extra input":
				d.Inputs = append(d.Inputs, toolchain.PythonInput{Path: pa315Project + "/requirements.txt", SHA256: files[pa315Project+"/requirements.txt"]})
			case "wrong input hash":
				d.Inputs[1].SHA256 = pa315Image
			case "missing canonical":
				d.CanonicalInputs = d.CanonicalInputs[:1]
			case "bad canonical hash":
				d.CanonicalInputs[0].SHA256 = "missing"
			case "bad recipe":
				d.Recipe = "unknown"
			case "bad platform":
				d.Platform = "linux/arm64"
			case "bad base":
				d.Base = "python:latest"
			case "bad wheel":
				d.Artifacts[0].Filename = "other-24.2-py3-none-any.whl"
			case "bad version":
				d.Artifacts[0].Version = "99"
			case "bad wheel hash":
				d.Artifacts[0].SHA256 = "unknown"
			}
			o := PythonApplicationSessionOptions{SelectImage: func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				return pa315Image, d, nil
			}}
			s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), o)
			if mutation != "none" {
				if s != nil || err == nil {
					t.Fatalf("invalid %s accepted", mutation)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				before := s.Provenance()
				d.Image = "changed"
				d.Inputs[0].SHA256 = "changed"
				view := s.Provenance()
				view.Dependencies.Artifacts[0].SHA256 = "changed"
				if !reflect.DeepEqual(before, s.Provenance()) {
					t.Fatal("dependency evidence aliases caller")
				}
			}
			noTools()
		})
	}
}
