package plumbing

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPytestImportReachabilityAnalysis(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("source analysis regression needs Python")
	}
	root := t.TempDir()
	write468(t, root, "runner.py", string(pytestRunner))
	cases := []struct {
		body string
		want bool
	}{
		{"assert future() == 7", true},
		{"value = future()\n    assert value == 7", true},
		{"\"docstring\"\n    future()", true},
		{"assert 7 == future()", true},
		{"return future()", true},
		{"return; future()", false},
		{"raise RuntimeError(); future()", false},
		{"if False: future()", false},
		{"if True: return\n    future()", false},
		{"for x in []: future()", false},
		{"while False: future()", false},
		{"try: future()\n    except: pass", false},
		{"def later(): future()", false},
		{"x = lambda: future()", false},
		{"x = [future() for i in []]", false},
		{"x = (future() for i in [1])", false},
		{"x = False and future()", false},
		{"x = future() if False else None", false},
		{"assert True or future()", false},
		{"future = lambda: 7\n    future()", false},
		{"unrelated()\n    future()", false},
		{"assert future(unrelated()) == 7", false},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			script := "import ast, runpy, sys\nn = runpy.run_path(sys.argv[1])\nf = ast.parse(sys.argv[2]).body[0]\nprint(n['unconditional_use'](f, 'future'))\n"
			cmd := exec.CommandContext(t.Context(), python, "-I", "-B", "-c", script, filepath.Join(root, "runner.py"), "def test_ready():\n    "+tc.body+"\n")
			out, err := cmd.CombinedOutput()
			if err != nil || (strings.TrimSpace(string(out)) == "True") != tc.want {
				t.Fatalf("reachability want=%v: %v %s", tc.want, err, out)
			}
		})
	}
}

// Supplement the pinned Docker matrix when pytest is already installed locally.
// This exercises the actual embedded framework observer, including exception
// chains and hook ordering, without manufacturing framework verdicts.
func TestPytestInstalledFrameworkRegression(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("local framework regression needs Python")
	}
	if out, err := exec.CommandContext(t.Context(), python, "-I", "-c", "import pytest; assert pytest.__version__ == '8.3.5'").CombinedOutput(); err != nil {
		t.Skipf("local pytest 8.3.5 is unavailable: %v %s; the enabled offline matrix requires it", err, out)
	}
	for _, tc := range []struct {
		name, source, conftest, status, kind string
	}{
		{"assertion", pySource617, "", "failed", "test"},
		{"shadow framework", pySource617, "", "failed", "test"},
		{"pass", "def test_ready(): assert True\n", "", "passed", "test"},
		{"import", "from product import future\ndef test_ready(): assert future() == 7\n", "", "failed", "compile"},
		{"unused import", "from product import future\ndef test_ready(): return; future()\n", "", "", "setup"},
		{"rebound import", "from product import future\nfuture = None\ndef test_ready(): assert future() == 7\n", "", "", "setup"},
		{"uncertain fixture", "from product import future\ndef test_ready(missing): assert future() == 7\n", "", "", "setup"},
		{"later import", "from product import future\nimport missing_distribution_regression\ndef test_ready(): assert future() == 7\n", "", "", "setup"},
		{"skip", "import pytest\ndef test_ready(): pytest.skip('skip')\n", "", "", "test"},
		{"fixture", "def test_ready(missing): assert False\n", "", "", "setup"},
		{"forged report", "def test_ready(): assert True\n", "import pytest\n@pytest.hookimpl(hookwrapper=True)\ndef pytest_runtest_makereport(item, call):\n    result = yield\n    if call.when == 'call': result.get_result().outcome = 'failed'\n", "", "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write468(t, root, "runner.py", string(pytestRunner))
			write468(t, root, "product.py", "VALUE = 0\n")
			write468(t, root, "test_ready.py", tc.source)
			if tc.name == "shadow framework" {
				write468(t, root, "pytest.py", "raise RuntimeError('candidate must not provide the framework')\n")
			}
			if tc.conftest != "" {
				write468(t, root, "conftest.py", tc.conftest)
			}
			in := RepositoryAcceptanceInvocation{Binding: RepositoryAcceptanceBinding{InvocationID: "fresh", Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::test_ready", SourceDigest: dependencyHash([]byte(tc.source)), ExecutionDigest: hash468("configuration")}, Source: tc.source}
			data, _ := json.Marshal(map[string]any{"Binding": in.Binding, "File": "test_ready.py", "Args": []string{"--import-mode=importlib", "-q", in.Binding.Selector}})
			events, err := os.CreateTemp(t.TempDir(), "events")
			if err != nil {
				t.Fatal(err)
			}
			defer events.Close()
			cmd := exec.CommandContext(t.Context(), python, "-I", "-B", filepath.Join(root, "runner.py"), string(data))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), pytestEventEnv+"=/dev/fd/3")
			cmd.ExtraFiles = []*os.File{events}
			diagnostic, _ := cmd.CombinedOutput()
			wire, err := os.ReadFile(events.Name())
			if err != nil || cmd.ProcessState == nil {
				t.Fatalf("framework did not execute: %v %s", err, diagnostic)
			}
			out := RepositoryProcessOutput{Events: wire, Stdout: diagnostic, ExitCode: cmd.ProcessState.ExitCode(), Started: true, Exited: true, Drained: true}
			evidence, err := collectPytestAcceptance(in, out)
			last := evidence.Records[len(evidence.Records)-1]
			if last.Kind != tc.kind || tc.status != "" && (err != nil || !evidence.Complete || last.Status != tc.status) || tc.status == "" && err == nil {
				t.Fatalf("framework outcome: %v %+v\n%s\n%s", err, evidence, wire, diagnostic)
			}
			if tc.status == "" {
				return
			}
			for _, damage := range []string{"replay", "duplicate", "truncated", "console", "not drained"} {
				bad := out
				switch damage {
				case "replay":
					bad.Events = bytes.ReplaceAll(wire, []byte(`"fresh"`), []byte(`"stale"`))
				case "duplicate":
					bad.Events = append(bytes.Clone(wire), wire...)
				case "truncated":
					bad.Events = wire[:len(wire)/2]
				case "console":
					bad.Events, bad.Stdout = nil, wire
				case "not drained":
					bad.Drained = false
				}
				if e, err := collectPytestAcceptance(in, bad); err == nil || e.Complete {
					t.Fatalf("%s evidence qualified: %+v", damage, e)
				}
			}
		})
	}
}
