package plumbing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

const pytestCallableError = "class LocalError(Exception):\n def __call__(self): return 7\n"

func pytestShadowedImports() []pyCase617 {
	return []pyCase617{
		{"worker", "test_ready.py", "test_ready", "from product import future\n" + pytestCallableError + "def test_ready():\n try: raise LocalError()\n except LocalError as future: assert future() == 7\n"},
		{"worker", "test_ready.py", "test_ready", "from product import future as local\n" + pytestCallableError + "def test_ready():\n try: raise LocalError()\n except LocalError as local: assert local() == 7\n"},
		{"worker", "test_ready.py", "TestReady::test_ready", "from product import future\n" + pytestCallableError + "class TestReady:\n def test_ready(self):\n  try: raise LocalError()\n  except LocalError as future: assert future() == 7\n"},
		{"worker", "test_ready.py", "test_ready", pytestCallableError + "def test_ready():\n from product import future\n try: raise LocalError()\n except LocalError as future: assert future() == 7\n"},
		{"worker", "test_ready.py", "test_ready", "from product import future\ndef test_ready():\n match (lambda: 7):\n  case future: assert future() == 7\n"},
		{"worker", "test_ready.py", "test_ready", "from product import future\ndef test_ready():\n match [lambda: 7]:\n  case [future]: assert future() == 7\n"},
	}
}

func TestPytestMissingAPIExceptionBindings(t *testing.T) {
	python := pythonForTransportTest(t)
	helper := filepath.Join(t.TempDir(), "runner.py")
	write468(t, filepath.Dir(helper), "runner.py", string(pytestRunner))
	for i, tc := range pytestShadowedImports() {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			root := t.TempDir()
			write468(t, root, tc.file, tc.source)
			write468(t, root, "product.py", "VALUE = 0\n")
			probe := `import ast, pathlib, runpy, sys
runner = runpy.run_path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
sys.path.insert(0, str(root))
source = root / 'test_ready.py'
data = source.read_bytes()
tree = ast.parse(data)
selector = 'test_ready.py::' + sys.argv[3]
function = runner['definition'](tree, selector)
def invoke(scope):
    names = sys.argv[3].split('::')
    if len(names) == 2:
        getattr(scope[names[0]](), names[1])()
    else:
        scope[names[0]]()
try:
    scope = {}
    exec(compile(data, str(source), 'exec'), scope)
    invoke(scope)
except ImportError as error:
    reason = runner['missing_api'](error, source, tree, function, root, selector)
    assert not reason, reason
else:
    raise AssertionError('fixture must fail at the missing import')
# No implementation of the proposed API is needed for this test to pass.
sys.modules['product'].future = None
scope = {}
exec(compile(data, str(source), 'exec'), scope)
invoke(scope)
`
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, python, "-I", "-c", probe, helper, root, tc.selector).CombinedOutput(); err != nil {
				t.Fatalf("shadowed import attribution: %v %s", err, out)
			}
		})
	}
}

type pytestExitCase struct {
	name, config, flag, mutation string
}

func pytestExitCases() []pytestExitCase {
	return []pytestExitCase{
		{"unchanged", "pytest.ini", "", "pass"},
		{"conftest", "pytest.ini", "", "Path('conftest.py').write_text('# changed after finish\\n')"},
		{"standard config", "pytest.ini", "", "Path('pytest.ini').write_text('[pytest]\\n# changed after finish\\n')"},
		{"custom config", "custom.ini", "-c", "Path('custom.ini').write_text('[pytest]\\n# changed after finish\\n')"},
		{"custom equals", "custom.ini", "-c=custom.ini", "Path('custom.ini').unlink()"},
		{"new conftest", "pytest.ini", "", "Path('nested').mkdir(); Path('nested/conftest.py').write_text('# added after finish\\n')"},
		{"deleted config", "pytest.ini", "", "Path('pytest.ini').unlink()"},
		{"linked config", "pytest.ini", "", "Path('pytest.ini').unlink(); Path('pytest.ini').symlink_to('custom.ini')"},
		{"rewritten helper", "pytest.ini", "", "Path(__import__('__main__').__file__).write_text(\"print('forged fingerprint')\\n\"); Path('pytest.ini').write_text('[pytest]\\n# changed after finish\\n')"},
	}
}

func pytestExitConftest(tc pytestExitCase) string {
	return "import atexit\nfrom pathlib import Path\ndef mutate():\n " + tc.mutation + "\natexit.register(mutate)\n"
}

func pytestExitArgs(tc pytestExitCase, selector string) []string {
	return append([]string{"pytest"}, pytestRepairArgs(pytestRepairCase{configFlag: tc.flag}, selector)...)
}

// Reuse the local byte transport, resolving Python from the host only in this
// test. No fixture supplies pytest observations or manufactured completion.
type pytestLocalFinalization struct {
	*localGoTransport
	python string
}

func (e *pytestLocalFinalization) arguments(argv []string) []string {
	argv = slices.Clone(argv)
	if argv[0] == "python3" {
		argv[0] = e.python
	}
	return argv
}

func (e *pytestLocalFinalization) Run(ctx context.Context, argv []string) ([]byte, error) {
	return e.localGoTransport.Run(ctx, e.arguments(argv))
}

func (e *pytestLocalFinalization) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	spec.Argv = e.arguments(spec.Argv)
	return e.localGoTransport.Prepare(ctx, spec)
}

func pytestFinalizationTransport(t *testing.T, python string) *pytestLocalFinalization {
	t.Helper()
	if err := localProcessReaper(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	e := &pytestLocalFinalization{localGoTransport: &localGoTransport{root: root, work: filepath.Join(root, "worker"), helpers: filepath.Join(root, "helpers"), env: dependencyEnv("python", false)}, python: python}
	for _, path := range []string{e.work, e.helpers} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return e
}

func TestPytestConfigurationAfterProcessExit(t *testing.T) {
	python := pythonForTransportTest(t)
	if out, err := exec.Command(python, "-I", "-c", "import pytest; assert pytest.__version__.split('.')[0] == '8'").CombinedOutput(); err != nil {
		t.Skipf("local pytest 8 unavailable; finalization also runs in the pinned offline opt-in: %v %s", err, out)
	}
	for _, tc := range pytestExitCases() {
		for _, value := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/value=%d", tc.name, value), func(t *testing.T) {
				e := pytestFinalizationTransport(t, python)
				write468(t, e.work, "test_ready.py", pySource617)
				write468(t, e.work, "product.py", fmt.Sprintf("VALUE = %d\n", value))
				write468(t, e.work, tc.config, "[pytest]\n")
				write468(t, e.work, "conftest.py", pytestExitConftest(tc))
				in := RepositoryAcceptanceInvocation{Binding: RepositoryAcceptanceBinding{InvocationID: t.Name(), Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::test_ready", SourceDigest: dependencyHash([]byte(pySource617)), ExecutionDigest: dependencyHash([]byte("captured"))}, Source: pySource617}
				in.Argv = pytestExitArgs(tc, in.Binding.Selector)
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				evidence, out, err := runRepositoryPytestAcceptance(ctx, part617("worker", "unused"), in, e)
				result, _ := repositoryEvidence(in.Binding, evidence, TestResult{})
				if tc.name == "unchanged" {
					want := "failed"
					if value == 7 {
						want = "passed"
					}
					if err != nil || result.Status != want || result.Kind != "test" {
						t.Fatalf("normal completion changed: %v %+v %s", err, result, out)
					}
				} else if err == nil || result.Status != "refused" || evidence.Complete {
					t.Fatalf("late configuration mutation qualified: %v %+v %s", err, result, out)
				}
			})
		}
	}
}

func TestPytestOfflineFinalization617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	for i, tc := range pytestShadowedImports() {
		t.Run(fmt.Sprint("shadow/", i), func(t *testing.T) {
			f := fixture617(t, image, platform, []pyCase617{tc}, nil, nil)
			c := check617(t, f, false)
			if result := result617(t, c.Run, f.plan.Tests[0]); result.Kind != "setup" || result.Status != "refused" {
				t.Fatalf("shadowed import qualified: %+v", result)
			}
			write468(t, f.candidate, "worker/product.py", "future = None\n")
			pass617(t, f)
		})
	}
	for _, tc := range pytestExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]string{"worker/" + tc.config: "[pytest]\n", "worker/conftest.py": pytestExitConftest(tc)}
			f := fixture617(t, image, platform, control617(), extra, func(c *repository.Config) {
				c.Parts[0].Commands["test_one"] = repository.Command{Argv: pytestExitArgs(tc, "{test}")}
			})
			check617(t, f, tc.name == "unchanged")
			fix617(t, f)
			r, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
			if tc.name == "unchanged" {
				if err != nil || !r.Passed() {
					t.Fatalf("unchanged configuration refused: %v %s", err, r.Summary())
				}
			} else if r.Passed() || result617(t, r, f.plan.Tests[0]).Status != "refused" {
				t.Fatalf("late mutation passed: %v %s", err, r.Summary())
			}
		})
	}
}

func TestPytestConfigurationProbeAvoidsCandidateImports(t *testing.T) {
	python := pythonForTransportTest(t)
	e := pytestFinalizationTransport(t, python)
	// This mode requires only the interpreter. Both code and arguments come
	// from the host, even if the candidate offers replacement helper modules.
	for _, name := range []string{"sitecustomize.py", "pytest.py", "json.py", "conftest.py"} {
		write468(t, e.work, name, "raise RuntimeError('candidate import is forbidden')\n")
	}
	write468(t, e.work, "custom.ini", "[pytest]\n")
	args := []string{"python3", "-I", "-S", "-c", string(pytestRunner), "--configuration", `{"Argv":["-c=custom.ini"]}`}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	before, err := e.Run(ctx, args)
	if err != nil || !executionDigest.MatchString(strings.TrimSuffix(string(before), "\n")) {
		t.Fatalf("isolated configuration probe: %v %s", err, before)
	}
	if out, err := e.Run(ctx, []string{"python3", "-I", "-S", "-c", "import atexit; from pathlib import Path; atexit.register(lambda: Path('custom.ini').write_text('[pytest]\\n# late change\\n'))"}); err != nil {
		t.Fatalf("exit handler: %v %s", err, out)
	}
	after, err := e.Run(ctx, args)
	if err != nil || string(before) == string(after) {
		t.Fatalf("late configuration mutation was not observed: %v %s", err, after)
	}
}
