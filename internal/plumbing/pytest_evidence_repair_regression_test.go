package plumbing

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func TestPytestFunctionExitObservations(t *testing.T) {
	python := pythonForTransportTest(t)
	helper := filepath.Join(t.TempDir(), "runner.py")
	write468(t, filepath.Dir(helper), "runner.py", string(pytestRunner))
	probe := `import runpy, sys
observe = runpy.run_path(sys.argv[1])['FunctionCalls']
for body, raises in [
    ('return None', False),
    ('assert False', True),
    ('raise RuntimeError("environment")', True),
    ('try:\n    assert False\nexcept AssertionError:\n    pass', False),
    ('try:\n    assert False\nfinally:\n    return None', False),
    ('try:\n    assert False\nfinally:\n    pass', True),
    ('try:\n    assert False\nexcept AssertionError:\n    raise ValueError("replacement")', True),
]:
    scope = {}
    exec('def selected():\n' + '\n'.join('    ' + line for line in body.splitlines()), scope)
    selected = scope['selected']
    caught = None
    with observe(selected.__code__) as calls:
        try:
            selected()
        except BaseException as error:
            caught = error  # A swallowing wrapper must not erase this exit.
    assert calls.intact and calls.calls == 1 and calls.errors == [caught], (body, calls.errors)
    assert (caught is not None) == raises, body
assert sys.gettrace() is None
with observe(selected.__code__) as calls:
    sys.settrace(None)
assert not calls.intact
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, python, "-I", "-c", probe, helper).CombinedOutput(); err != nil {
		t.Fatalf("independent function outcomes: %v %s", err, out)
	}
}

func TestPytestMissingAPIClassBindings(t *testing.T) {
	python := pythonForTransportTest(t)
	helper := filepath.Join(t.TempDir(), "runner.py")
	write468(t, filepath.Dir(helper), "runner.py", string(pytestRunner))
	for _, tc := range []struct {
		name, source, selector string
		eligible               bool
	}{
		{"module class", "from product import future\nclass future: pass\ndef test_ready(): future()\n", "test_ready", false},
		{"aliased class", "from product import future as local\nclass local: pass\ndef test_ready(): local()\n", "test_ready", false},
		{"selected class", "from product import future as TestReady\nclass TestReady:\n def test_ready(self): TestReady()\n", "TestReady::test_ready", false},
		{"local class", "def test_ready():\n from product import future\n class future: pass\n future()\n", "test_ready", false},
		{"conditional class", "from product import future\nif True:\n class future: pass\ndef test_ready(): future()\n", "test_ready", false},
		{"method shadow", "from product import future\nclass future: pass\nclass TestReady:\n def test_ready(self): future()\n", "TestReady::test_ready", false},
		{"unrelated class scope", "from product import future\nclass Other:\n class future: pass\ndef test_ready(): future()\n", "test_ready", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write468(t, root, "product.py", "VALUE = 0\n")
			write468(t, root, "test_ready.py", tc.source)
			probe := `import ast, pathlib, runpy, sys
runner = runpy.run_path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
sys.path.insert(0, str(root))
source = root / 'test_ready.py'
data = source.read_bytes()
tree = ast.parse(data)
selector = 'test_ready.py::' + sys.argv[3]
function = runner['definition'](tree, selector)
scope = {}
try:
    exec(compile(data, str(source), 'exec'), scope)
    scope['test_ready']()
except ImportError as error:
    reason = runner['missing_api'](error, source, tree, function, root, selector)
    assert bool(reason) == (sys.argv[4] == 'true'), reason
else:
    raise AssertionError('fixture must fail at the missing import')
`
			want := "false"
			if tc.eligible {
				want = "true"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, python, "-I", "-c", probe, helper, root, tc.selector, want).CombinedOutput(); err != nil {
				t.Fatalf("class binding attribution: %v %s", err, out)
			}
		})
	}
}

func TestPytestCustomConfigurationFingerprint(t *testing.T) {
	python := pythonForTransportTest(t)
	helper, root := t.TempDir(), t.TempDir()
	write468(t, helper, "runner.py", string(pytestRunner))
	write468(t, root, "custom.ini", "[pytest]\n")
	probe := `import pathlib, runpy, sys
configuration = runpy.run_path(sys.argv[1])['configuration']
root = pathlib.Path(sys.argv[2])
path = root / 'custom.ini'
before = configuration(root, path)
assert before == configuration(root, path)
path.write_text('[pytest]\n# changed during execution\n')
assert before != configuration(root, path), 'custom configuration escaped the binding'
path.unlink()
try:
    configuration(root, path)
except OSError:
    pass
else:
    raise AssertionError('missing effective configuration was accepted')
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, python, "-I", "-c", probe, filepath.Join(helper, "runner.py"), root).CombinedOutput(); err != nil {
		t.Fatalf("effective configuration binding: %v %s", err, out)
	}
}

type pytestRepairCase struct {
	name, source, conftest, configFlag string
	status                             string
}

func pytestRepairCases() []pytestRepairCase {
	return []pytestRepairCase{
		{"swallowed assertion", "def test_ready(): assert False\n", "import pytest\n@pytest.hookimpl(wrapper=True)\ndef pytest_runtest_call(item):\n try:\n  yield\n except AssertionError:\n  pass\n", "", "refused"},
		{"forced wrapper result", "def test_ready(): assert False\n", "import pytest\n@pytest.hookimpl(hookwrapper=True)\ndef pytest_runtest_call(item):\n result = yield\n result.force_result(None)\n", "", "refused"},
		{"invented assertion", "def test_ready(): pass\n", "import pytest\n@pytest.hookimpl(wrapper=True)\ndef pytest_runtest_call(item):\n yield\n assert False, 'hook failure'\n", "", "refused"},
		{"replaced assertion", "def test_ready(): assert False\n", "import pytest\n@pytest.hookimpl(wrapper=True)\ndef pytest_runtest_call(item):\n try:\n  yield\n except AssertionError:\n  raise AssertionError('replacement')\n", "", "refused"},
		{"handled assertion", "def test_ready():\n try:\n  assert False\n except AssertionError:\n  pass\n", "", "", "passed"},
		{"finally return", "def test_ready():\n try:\n  assert False\n finally:\n  return\n", "", "", "passed"},
		{"class shadow", "from product import future\nclass future: pass\ndef test_ready(): future()\n", "", "", "refused"},
		{"custom unchanged", pySource617, "", "-c", "failed"},
		{"custom equals unchanged", pySource617, "", "-c=custom.ini", "failed"},
		{"custom changed in configure", pySource617, "from pathlib import Path\ndef pytest_configure(config):\n Path('custom.ini').write_text('[pytest]\\n# changed\\n')\n", "-c", "refused"},
		{"custom changed during call", "from pathlib import Path\nimport product\ndef test_ready():\n Path('custom.ini').write_text('[pytest]\\n# changed\\n')\n assert product.VALUE == 7\n", "", "-c=custom.ini", "refused"},
		{"custom changed at session end", pySource617, "from pathlib import Path\ndef pytest_sessionfinish(session, exitstatus):\n Path('custom.ini').write_text('[pytest]\\n# changed\\n')\n", "-c", "refused"},
	}
}

func pytestRepairArgs(tc pytestRepairCase, selector string) []string {
	args := []string{"--import-mode=importlib", "-q"}
	if tc.configFlag != "" {
		args = append(args, tc.configFlag)
		if tc.configFlag == "-c" {
			args = append(args, "custom.ini")
		}
	}
	return append(args, selector)
}

func TestPytestEvidenceRepairsRealLocal(t *testing.T) {
	python := pythonForTransportTest(t)
	if out, err := exec.Command(python, "-I", "-c", "import pytest; assert pytest.__version__.split('.')[0] == '8'").CombinedOutput(); err != nil {
		t.Skipf("local pytest 8 unavailable; repairs also run in the pinned offline opt-in: %v %s", err, out)
	}
	for _, tc := range pytestRepairCases() {
		t.Run(tc.name, func(t *testing.T) {
			root, helpers := t.TempDir(), t.TempDir()
			write468(t, root, "test_ready.py", tc.source)
			write468(t, root, "product.py", "VALUE = 0\n")
			if tc.conftest != "" {
				write468(t, root, "conftest.py", tc.conftest)
			}
			if tc.configFlag != "" {
				write468(t, root, "custom.ini", "[pytest]\n")
			}
			binding := RepositoryAcceptanceBinding{InvocationID: t.Name(), Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::test_ready", SourceDigest: dependencyHash([]byte(tc.source)), ExecutionDigest: dependencyHash([]byte("captured"))}
			manifest, err := json.Marshal(map[string]any{"Binding": binding, "File": "test_ready.py", "Argv": pytestRepairArgs(tc, binding.Selector)})
			if err != nil {
				t.Fatal(err)
			}
			write468(t, helpers, "invocation.json", string(manifest))
			write468(t, helpers, "runner.py", string(pytestRunner))
			out := pythonTransportTest(t, root, python, []string{python, "-I", filepath.Join(helpers, "runner.py"), filepath.Join(helpers, "invocation.json")})
			evidence, _ := collectPytestAcceptance(binding, out, nil)
			result, _ := repositoryEvidence(binding, evidence, TestResult{})
			if result.Status != tc.status {
				t.Fatalf("want %s, got %+v\n%s\n%s\n%s", tc.status, result, out.Events, out.Stdout, out.Stderr)
			}
		})
	}
}

func TestPytestOfflineEvidenceRepairs617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	for _, tc := range pytestRepairCases() {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]string{}
			if tc.conftest != "" {
				extra["worker/conftest.py"] = tc.conftest
			}
			if tc.configFlag != "" {
				extra["worker/custom.ini"] = "[pytest]\n"
			}
			f := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", "test_ready", tc.source}}, extra, func(c *repository.Config) {
				c.Parts[0].Commands["test_one"] = repository.Command{Argv: append([]string{"pytest"}, pytestRepairArgs(tc, "{test}")...)}
			})
			c := check617(t, f, tc.status == "failed")
			if v := result617(t, c.Run, f.plan.Tests[0]); v.Status != tc.status {
				t.Fatalf("want %s, got %+v", tc.status, v)
			}
			fix617(t, f)
			r, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
			// Shadowing can pass after an API is added, which is precisely why
			// its original import failure must never qualify the draft.
			want := tc.status != "refused" || tc.name == "class shadow"
			if r.Passed() != want {
				t.Fatalf("candidate completion want=%v: %v\n%s", want, err, r.Summary())
			}
		})
	}
}
