package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPytestImportAttributionUsesRealExceptions(t *testing.T) {
	root := t.TempDir()
	write468(t, root, "runner.py", string(pytestRunner))
	write468(t, root, "product.py", "VALUE = 0\n")
	script := `import ast, importlib.util, pathlib, sys
root = pathlib.Path.cwd()
spec = importlib.util.spec_from_file_location('integration', root / 'runner.py')
integration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(integration)
sys.path.insert(0, str(root))
cases = [
 ('assert future() == 7', True),
 ('future()', True),
 ('value = future()', True),
 ('value = 1\nassert future() == 7', True),
 ('return; future()', False),
 ('raise RuntimeError()\nfuture()', False),
 ('if False:\n    future()', False),
 ('if True:\n    return\nfuture()', False),
 ('for item in []:\n    future()', False),
 ('while False:\n    future()', False),
 ('try:\n    future()\nexcept Exception:\n    pass', False),
 ('def later():\n    future()', False),
 ('later = lambda: future()', False),
 ('value = [future() for item in []]', False),
 ('value = (future() for item in [1])', False),
 ('value = False and future()', False),
 ('value = future() if False else None', False),
 ('assert True or future()', False),
 ('assert False\nfuture()', False),
 ('other()\nfuture()', False),
 ('future = lambda: 7\nassert future() == 7', False),
 ('assert False\ndef neighbor():\n    future()', False),
]
file = root / 'test_ready.py'
for body, want in cases:
 for method in (False, True):
    prefix = 'from product import future\n'
    indent = '        ' if method else '    '
    prefix += 'class TestReady:\n    def test_ready(self):\n' if method else 'def test_ready():\n'
    source = prefix + '\n'.join(indent + line for line in body.splitlines()) + '\n'
    file.write_text(source)
    try:
        exec(compile(source, str(file), 'exec'), {})
    except ImportError as error:
        selector = 'test_ready.py::TestReady::test_ready' if method else 'test_ready.py::test_ready'
        got = integration.missing_api(error, source, file, selector, root)
        assert bool(got) == want, (body, method, got, want)
    else:
        raise AssertionError('fixture did not raise its real missing import')
for extra in ("future = lambda: 7\n", "def other(x=globals().update(future=lambda: 7)): pass\n"):
    source = 'from product import future\n' + extra + 'def test_ready(): assert future() == 7\n'
    file.write_text(source)
    try:
        exec(compile(source, str(file), 'exec'), {})
    except ImportError as error:
        assert integration.missing_api(error, source, file, 'test_ready.py::test_ready', root) is None
`
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-B", "-c", script)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real Python import attribution: %v\n%s", err, out)
	}
}

func TestPytestEvidenceRejectsConflictingLifecycle(t *testing.T) {
	in := RepositoryAcceptanceInvocation{Binding: RepositoryAcceptanceBinding{InvocationID: "fresh", Part: "app", Framework: "pytest", File: "app/test_ready.py", Selector: "test_ready.py::test_ready", SourceDigest: dependencyHash([]byte("source")), ExecutionDigest: dependencyHash([]byte("policy"))}}
	for _, mode := range []string{"valid", "replay", "no call", "fixture", "forged report", "xfail", "duplicate collection", "session", "source", "configuration", "process", "transport", "duplicate", "truncate"} {
		t.Run(mode, func(t *testing.T) {
			state := &pytestState{Collected: []string{in.Binding.Selector}, Calls: 1, Session: 1, Kind: "test", SourceDigest: in.Binding.SourceDigest, ConfigBefore: dependencyHash(nil), ConfigAfter: dependencyHash(nil)}
			for _, when := range []string{"setup", "call", "teardown"} {
				state.Phases = append(state.Phases, pytestPhase{Node: in.Binding.Selector, When: when, Raw: "passed", Outcome: "passed"})
			}
			binding := in.Binding
			var runErr error
			switch mode {
			case "replay":
				binding.InvocationID = "old"
			case "no call":
				state.Calls = 0
			case "fixture":
				state.Phases[2].Raw, state.Phases[2].Outcome, state.Phases[2].Exception = "failed", "failed", "builtins.RuntimeError"
			case "forged report":
				state.Phases[1].Outcome = "failed"
			case "xfail":
				state.Phases[1].Xfail = true
			case "duplicate collection":
				state.Collected = append(state.Collected, in.Binding.Selector)
			case "session":
				state.Session = 0
			case "source":
				state.SourceDigest = dependencyHash([]byte("changed"))
			case "configuration":
				state.ConfigAfter = dependencyHash([]byte("changed"))
			case "transport":
				runErr = errors.New("broken connection")
			}
			var events bytes.Buffer
			encoder := json.NewEncoder(&events)
			_ = encoder.Encode(pytestEvent{Event: "start", Binding: binding})
			_ = encoder.Encode(pytestEvent{Event: "finish", Binding: binding, State: state})
			out := RepositoryProcessOutput{Started: true, Exited: true, Drained: mode != "process", Events: events.Bytes()}
			if mode == "duplicate" {
				out.Events = append(bytes.Clone(out.Events), out.Events...)
			}
			if mode == "truncate" {
				out.Events = out.Events[:len(out.Events)-1]
			}
			evidence, err := collectPytestAcceptance(in, out, runErr)
			if (err == nil && evidence.Complete) != (mode == "valid") {
				t.Fatalf("unexpected evidence: %v %+v", err, evidence)
			}
			if mode == "fixture" && evidence.Records[0].Kind != "setup" {
				t.Fatal("fixture failure was not setup")
			}
		})
	}
}

// Supplemental local framework coverage; the pinned Docker acceptance matrix
// remains the required opt-in and fails, rather than skips, when enabled.
func TestPytestLocalFrameworkRegression(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(python, "-I", "-c", "import pytest").CombinedOutput(); err != nil {
		t.Skipf("local pytest unavailable; pinned offline matrix has a separate opt-in: %s", out)
	}
	for _, tc := range []struct{ name, source, config, kind, status string }{
		{"pass", "def test_ready(): assert True\n", "", "test", "passed"},
		{"assertion", "def test_ready(): assert False\n", "", "test", "failed"},
		{"import", "from product import future\ndef test_ready(): assert future() == 7\n", "", "compile", "failed"},
		{"fixture", "def test_ready(missing): assert False\n", "", "setup", "refused"},
		{"skip", "import pytest\ndef test_ready(): pytest.skip('skip')\n", "", "test", "refused"},
		{"xfail", "import pytest\n@pytest.mark.xfail\ndef test_ready(): assert False\n", "", "test", "refused"},
		{"forged report", "def test_ready(): assert True\n", "import pytest\n@pytest.hookimpl(hookwrapper=True)\ndef pytest_runtest_makereport(item, call):\n    result = yield\n    if call.when == 'call': result.get_result().outcome = 'failed'\n", "test", "refused"},
		{"late configuration mutation", "import atexit, pathlib\ndef test_ready():\n    atexit.register(lambda: pathlib.Path('pytest.ini').write_text('[pytest]\\naddopts = -s\\n'))\n", "", "test", "refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write468(t, root, "runner.py", string(pytestRunner))
			write468(t, root, "test_ready.py", tc.source)
			write468(t, root, "product.py", "VALUE=0\n")
			if tc.config != "" {
				write468(t, root, "conftest.py", tc.config)
			}
			in := RepositoryAcceptanceInvocation{Binding: RepositoryAcceptanceBinding{InvocationID: "local", Part: "app", Framework: "pytest", File: "app/test_ready.py", Selector: "test_ready.py::test_ready", SourceDigest: dependencyHash([]byte(tc.source)), ExecutionDigest: dependencyHash([]byte("policy"))}}
			request, _ := json.Marshal(map[string]any{"Binding": in.Binding, "File": "test_ready.py", "Argv": []string{"--import-mode=importlib", "-q", in.Binding.Selector}})
			file, err := os.Create(filepath.Join(root, "events"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "-I", "-B", "runner.py", string(request))
			cmd.Dir, cmd.ExtraFiles = root, []*os.File{file}
			cmd.Env = append(os.Environ(), pytestEventEnv+"=/dev/fd/3", "PYTEST_DISABLE_PLUGIN_AUTOLOAD=1")
			diagnostic, runErr := cmd.CombinedOutput()
			_ = file.Close()
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			if cmd.ProcessState == nil {
				t.Fatalf("start local pytest: %v %s", runErr, diagnostic)
			}
			out := RepositoryProcessOutput{Started: true, Exited: true, Drained: true, ExitCode: cmd.ProcessState.ExitCode()}
			out.Events, err = os.ReadFile(filepath.Join(root, "events"))
			if err != nil {
				t.Fatal(err)
			}
			evidence, _ := collectPytestAcceptance(in, out, nil)
			result, _ := repositoryEvidence(in.Binding, evidence, TestResult{Framework: "pytest"})
			if result.Kind != tc.kind || result.Status != tc.status {
				t.Fatalf("framework result: %+v\nrun: %v\n%s\nevents: %s", result, runErr, diagnostic, out.Events)
			}
		})
	}
}
