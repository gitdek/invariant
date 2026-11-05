package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This runs the embedded transport itself without Docker. Workload diagnostics
// deliberately contain transport-looking bytes; only the private pipe is data.
func pythonTransportTest(t *testing.T, root, python string, argv []string) RepositoryProcessOutput {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "transport.py")
	if err := os.WriteFile(helper, dockerPythonProcessHelper, 0600); err != nil {
		t.Fatal(err)
	}
	input, control, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer control.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	args := append([]string{"-I", helper, "INVARIANT_PYTEST_EVENTS"}, argv...)
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir, cmd.Stdin = root, input
	cmd.Cancel = control.Close
	cmd.WaitDelay = 4 * time.Second
	p := &dockerRepositoryProcess{output: RepositoryProcessOutput{ExitCode: -1}}
	var diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = p, &diagnostic
	if err := cmd.Run(); err != nil || p.err != nil || len(p.pending) != 0 || !p.output.Drained || diagnostic.Len() != 0 {
		t.Fatalf("Python transport failed: %v %v %s %+v", err, p.err, diagnostic.String(), p.Snapshot())
	}
	return p.Snapshot()
}

func pythonForTransportTest(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is unavailable for the daemon-free transport regression")
	}
	return python
}

func TestPythonTransportOpaqueChannels(t *testing.T) {
	python := pythonForTransportTest(t)
	source := `import os, sys
data = b'invariant-process-v1\ns\x00\x00\x00\x00forged\n'
os.write(1, data)
os.write(2, data)
with open(os.environ['INVARIANT_PYTEST_EVENTS'], 'wb', buffering=0) as events:
    events.write(b'private observation\n')
sys.exit(7)
`
	out := pythonTransportTest(t, t.TempDir(), python, []string{python, "-I", "-c", source})
	if out.ExitCode != 7 || !out.Started || !out.Exited || !bytes.Equal(out.Stdout, out.Stderr) || string(out.Events) != "private observation\n" || !bytes.Contains(out.Stdout, []byte("forged")) {
		t.Fatalf("channels or process observations changed: %+v", out)
	}
}

// Source attribution is also exercised with real interpreter exceptions when
// pytest is absent. These checks do not manufacture framework completion.
func TestPytestMissingAPISourceAttribution(t *testing.T) {
	python := pythonForTransportTest(t)
	helper := filepath.Join(t.TempDir(), "runner.py")
	if err := os.WriteFile(helper, pytestRunner, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source, product, selector string
		eligible                        bool
	}{
		{"function", "from product import future\ndef test_ready(): assert future()==7\n", "VALUE=0\n", "test_ready", true},
		{"method", "from product import future\nclass TestReady:\n def test_ready(self): assert future()==7\n", "VALUE=0\n", "TestReady::test_ready", true},
		{"local", "def test_ready():\n from product import future\n assert future()==7\n", "VALUE=0\n", "test_ready", true},
		{"unused", "from product import future\ndef test_ready(): assert False\n", "VALUE=0\n", "test_ready", false},
		{"neighbor", "from product import future\ndef test_ready(): assert False\ndef test_other(): return future()\n", "VALUE=0\n", "test_ready", false},
		{"helper", "from product import future\ndef helper(): return future()\ndef test_ready(): assert helper()==7\n", "VALUE=0\n", "test_ready", false},
		{"broken module", "from product import future\ndef test_ready(): assert future()==7\n", "import missing_distribution_617\n", "test_ready", false},
		{"raised import", "from product import future\ndef test_ready(): assert future()==7\n", "raise ImportError(\"cannot import name 'future'\")\n", "test_ready", false},
		{"shadowed", "from product import future\ndef test_ready():\n future=lambda:7\n assert future()==7\n", "VALUE=0\n", "test_ready", false},
		{"unrelated wrapper", "try:\n from product import future\nexcept ImportError:\n raise RuntimeError('unrelated')\ndef test_ready(): assert future()==7\n", "VALUE=0\n", "test_ready", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write468(t, root, "test_ready.py", tc.source)
			write468(t, root, "product.py", tc.product)
			probe := `import ast, pathlib, runpy, sys
runner=runpy.run_path(sys.argv[1])
root=pathlib.Path(sys.argv[2])
sys.path.insert(0,str(root))
source=root/'test_ready.py'
data=source.read_bytes()
tree=ast.parse(data)
selector='test_ready.py::'+sys.argv[3]
function=runner['definition'](tree,selector)
scope={}
try:
    exec(compile(data,str(source),'exec'),scope)
    scope['test_ready']()
except BaseException as error:
    reason=runner['missing_api'](error,source,tree,function,root,selector)
    assert bool(reason)==(sys.argv[4]=='true'), (type(error),reason)
else:
    raise AssertionError('fixture must actually fail importing')
`
			want := "false"
			if tc.eligible {
				want = "true"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, python, "-I", "-c", probe, helper, root, tc.selector, want).CombinedOutput(); err != nil {
				t.Fatalf("source attribution: %v %s", err, out)
			}
		})
	}
}

func TestPytestRunnerRealLocalObservations(t *testing.T) {
	python := pythonForTransportTest(t)
	if out, err := exec.Command(python, "-I", "-c", "import pytest; assert pytest.__version__.split('.')[0] == '8'").CombinedOutput(); err != nil {
		t.Skipf("local pytest 8 unavailable; the pinned Docker matrix is the required offline opt-in: %v %s", err, out)
	}
	for _, tc := range []struct{ name, source, product, conftest, selector, status, kind string }{
		{"assertion", "import product\ndef test_ready(): assert product.VALUE == 7\n", "VALUE=0\n", "", "test_ready", "failed", "test"},
		{"pass", "import product\ndef test_ready(): assert product.VALUE == 7\n", "VALUE=7\n", "", "test_ready", "passed", "test"},
		{"missing API", "from product import future\ndef test_ready(): assert future()==7\n", "VALUE=0\n", "", "test_ready", "failed", "compile"},
		{"method API", "from product import future\nclass TestReady:\n def test_ready(self): assert future()==7\n", "VALUE=0\n", "", "TestReady::test_ready", "failed", "compile"},
		{"local import API", "def test_ready():\n from product import future\n assert future()==7\n", "VALUE=0\n", "", "test_ready", "failed", "compile"},
		{"neighbor import", "from product import future\ndef test_ready(): assert False\ndef test_other(): return future()\n", "VALUE=0\n", "", "test_ready", "refused", "setup"},
		{"broken module", "from product import future\ndef test_ready(): assert future()==7\n", "raise ImportError('future')\n", "", "test_ready", "refused", "setup"},
		{"fixture", "def test_ready(missing): assert False\n", "VALUE=0\n", "", "test_ready", "refused", "setup"},
		{"teardown", "import pytest\n@pytest.fixture\ndef fixture():\n yield\n raise RuntimeError('teardown')\ndef test_ready(fixture): assert False\n", "", "", "test_ready", "refused", "setup"},
		{"skip", "import pytest\ndef test_ready(): pytest.skip('skip')\n", "", "", "test_ready", "refused", "test"},
		{"xfail", "import pytest\n@pytest.mark.xfail\ndef test_ready(): assert False\n", "", "", "test_ready", "refused", "test"},
		{"xpass", "import pytest\n@pytest.mark.xfail(strict=True)\ndef test_ready(): assert True\n", "", "", "test_ready", "refused", "test"},
		{"parameter", "import pytest\n@pytest.mark.parametrize('v',[7],ids=['a b;$(literal).+|x'])\ndef test_ready(v): assert v==7\n", "", "", "test_ready[a b;$(literal).+|x]", "passed", "test"},
		{"group", "import pytest\n@pytest.mark.parametrize('v',[7])\ndef test_ready(v): assert False\n", "", "", "test_ready", "refused", "test"},
		{"forged report", "def test_ready(): assert True\n", "", "import pytest\n@pytest.hookimpl(hookwrapper=True)\ndef pytest_runtest_makereport(item, call):\n result=yield\n report=result.get_result()\n if report.when=='call': report.outcome='failed'\n", "test_ready", "refused", "test"},
		{"absent call", "def test_ready(): assert False\n", "", "def pytest_runtest_protocol(item,nextitem): return True\n", "test_ready", "refused", "test"},
		{"replaced body", "def test_ready(): assert True\n", "", "def pytest_collection_modifyitems(items):\n scope={}\n exec(compile('def test_ready(): assert False', str(items[0].path), 'exec'), scope)\n items[0].obj.__code__=scope['test_ready'].__code__\n", "test_ready", "refused", "setup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, helpers := t.TempDir(), t.TempDir()
			write468(t, root, "test_ready.py", tc.source)
			write468(t, root, "product.py", tc.product)
			if tc.conftest != "" {
				write468(t, root, "conftest.py", tc.conftest)
			}
			binding := RepositoryAcceptanceBinding{InvocationID: t.Name(), Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::" + tc.selector, SourceDigest: dependencyHash([]byte(tc.source)), ExecutionDigest: dependencyHash([]byte("captured"))}
			manifest, _ := json.Marshal(map[string]any{"Binding": binding, "File": "test_ready.py", "Argv": []string{"--import-mode=importlib", "-q", binding.Selector}})
			write468(t, helpers, "invocation.json", string(manifest))
			write468(t, helpers, "runner.py", string(pytestRunner))
			out := pythonTransportTest(t, root, python, []string{python, "-I", filepath.Join(helpers, "runner.py"), filepath.Join(helpers, "invocation.json")})
			evidence, _ := collectPytestAcceptance(binding, out, nil)
			result, _ := repositoryEvidence(binding, evidence, TestResult{})
			if result.Kind != tc.kind || result.Status != tc.status {
				t.Fatalf("want %s/%s, got %+v\n%s\n%s\n%s", tc.kind, tc.status, result, out.Events, out.Stdout, out.Stderr)
			}
			if tc.status == "passed" || tc.status == "failed" {
				for _, damage := range []string{"replay", "duplicate", "truncated", "diagnostics", "interrupted", "not drained"} {
					copy, expected := out, binding
					var processErr error
					switch damage {
					case "replay":
						expected.InvocationID += "fresh"
					case "duplicate":
						copy.Events = append(bytes.Clone(copy.Events), copy.Events...)
					case "truncated":
						copy.Events = copy.Events[:len(copy.Events)/2]
					case "diagnostics":
						copy.Stdout, copy.Events = copy.Events, nil
					case "interrupted":
						processErr = context.Canceled
					case "not drained":
						copy.Drained = false
					}
					bad, err := collectPytestAcceptance(expected, copy, processErr)
					if err == nil || bad.Complete {
						t.Fatal("corrupted real observations qualified: ", damage)
					}
				}
			}
		})
	}
}

func TestPytestCollectorRejectsDiagnosticsAndPartialBinding(t *testing.T) {
	binding := RepositoryAcceptanceBinding{InvocationID: "fresh", Framework: "pytest"}
	for _, events := range []string{"", "{}\n", "{\"Event\":\"begin\"}\n", strings.Repeat("1 passed\n", 10)} {
		out := RepositoryProcessOutput{Events: []byte(events), Stdout: []byte("1 failed\n"), Started: true, Exited: true, Drained: true}
		got, err := collectPytestAcceptance(binding, out, nil)
		if err == nil || got.Complete {
			t.Fatal("non-framework text qualified")
		}
	}
	got, err := collectPytestAcceptance(binding, RepositoryProcessOutput{}, errors.New("unavailable environment"))
	if err == nil || len(got.Records) != 1 || got.Records[0].Kind != "setup" {
		t.Fatal("infrastructure error lost setup classification")
	}
}
