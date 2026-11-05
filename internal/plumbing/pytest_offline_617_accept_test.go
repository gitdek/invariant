package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// All runtime verdicts in this file come from real pytest. The opt-in uses
// an already-local digest-pinned Python 3.13 image containing pytest 8.3.5.
// No fixture installs pytest, pulls an image, or treats absent tools as a skip.
func offline617(t *testing.T) (string, string, bool) {
	t.Helper()
	selection617(t) // Mandatory daemon-free control also fails on the old code.
	if os.Getenv("INVARIANT_PYTEST_ACCEPTANCE_REAL") != "1" {
		t.Log("set INVARIANT_PYTEST_ACCEPTANCE_REAL=1, INVARIANT_PYTEST_ACCEPTANCE_IMAGE=<repository>@sha256:<digest>, and INVARIANT_PYTEST_ACCEPTANCE_PLATFORM=linux/amd64 or linux/arm64 for real offline pytest")
		return "", "", false
	}
	image, platform := os.Getenv("INVARIANT_PYTEST_ACCEPTANCE_IMAGE"), os.Getenv("INVARIANT_PYTEST_ACCEPTANCE_PLATFORM")
	if !dependencyBase.MatchString(image) || !dependencyPlatform.MatchString(platform) {
		t.Fatal("enabled real pytest suite requires a digest-pinned image and explicit Linux platform")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("enabled real pytest suite requires Docker: ", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := inspectDependencyImage(ctx, image, platform); err != nil {
		t.Fatal("enabled real pytest suite requires the pinned image and daemon already available; nothing is downloaded: ", err)
	}
	out, err := dependencyDocker(ctx, "run", "--rm", "--pull", "never", "--network", "none", "--platform", platform, "--cap-drop", "ALL", "--entrypoint", "python3", image, "-I", "-c", "import sys, pytest; assert sys.version_info[:2] == (3, 13); assert pytest.__version__ == '8.3.5'")
	if err != nil {
		t.Fatalf("pinned offline fixture must contain real Python 3.13 and pytest 8.3.5: %v %s", err, out)
	}
	return image, platform, true
}

type pyCase617 struct{ part, file, selector, source string }
type pyFixture617 struct {
	root, candidate string
	plan            *Plan
	sandbox         Sandbox
	runner          *pyRunner617
}
type pyRunner617 struct {
	*DockerDependencyRunner
	t            *testing.T
	mu           sync.Mutex
	opens        []DependencyRequest
	closes       int
	active       int
	maxActive    int
	eventOutputs []RepositoryProcessOutput
	deadlines    []time.Time
	damage       func(RepositoryProcessOutput) RepositoryProcessOutput
	afterWait    func()
	onStart      func(context.Context, RepositoryProcess)
	beforeOpen   func(context.Context) error
	afterClose   func(context.Context) error
}

func (r *pyRunner617) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	if len(q.Argv) != 0 || q.Network {
		return DependencyArtifact{}, errors.New("pytest fixture cannot install or download dependencies")
	}
	q.Pull = false
	a, err := r.DockerDependencyRunner.Install(ctx, q)
	if a.Image != "" {
		r.t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
			defer cancel()
			if _, err := dependencyDocker(cleanup, "image", "rm", a.Image); err != nil {
				r.t.Error(err)
			}
		})
	}
	return a, err
}

func (r *pyRunner617) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	if q.Network || q.Pull || !q.Init || q.MemoryBytes != 4<<30 || q.PidsLimit != 1024 || q.Env["PIP_NO_INDEX"] != "1" || q.Env["GOTOOLCHAIN"] != "" {
		return nil, errors.New("pytest lost prepared Python isolation")
	}
	if r.beforeOpen != nil {
		if err := r.beforeOpen(ctx); err != nil {
			return nil, err
		}
	}
	e, err := r.DockerDependencyRunner.OpenChecks(ctx, q)
	if err != nil {
		return nil, err
	}
	transport, ok := e.(RepositoryProcessTransport)
	if !ok {
		_ = e.Close(context.Background())
		return nil, errors.New("prepared Python environment has no process transport")
	}
	r.mu.Lock()
	r.opens = append(r.opens, q)
	r.active++
	r.maxActive = max(r.active, r.maxActive)
	r.mu.Unlock()
	return &pyEnvironment617{RepositoryProcessTransport: transport, runner: r}, nil
}

type pyEnvironment617 struct {
	RepositoryProcessTransport
	runner *pyRunner617
	once   sync.Once
	err    error
}

func (e *pyEnvironment617) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	if spec.EventEnv != "" {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("pytest process lacks a deadline")
		}
		e.runner.mu.Lock()
		e.runner.deadlines = append(e.runner.deadlines, deadline)
		e.runner.mu.Unlock()
	}
	p, err := e.RepositoryProcessTransport.Prepare(ctx, spec)
	if p == nil {
		return nil, err
	}
	return &pyPrepared617{RepositoryPreparedProcess: p, runner: e.runner}, err
}
func (e *pyEnvironment617) Close(ctx context.Context) error {
	e.once.Do(func() {
		if end, ok := ctx.Deadline(); ctx.Err() != nil || !ok || time.Until(end) > SandboxCleanupAllowance+time.Second {
			e.runner.t.Error("cleanup lost its bounded uncanceled reserve")
		}
		e.err = e.RepositoryProcessTransport.Close(ctx)
		if e.runner.afterClose != nil {
			e.err = errors.Join(e.err, e.runner.afterClose(ctx))
		}
		e.runner.mu.Lock()
		e.runner.closes++
		e.runner.active--
		e.runner.mu.Unlock()
	})
	return e.err
}

type pyPrepared617 struct {
	RepositoryPreparedProcess
	runner *pyRunner617
}

func (p *pyPrepared617) Start(ctx context.Context) (RepositoryProcess, error) {
	r, err := p.RepositoryPreparedProcess.Start(ctx)
	if r == nil {
		return nil, err
	}
	if p.runner.onStart != nil {
		p.runner.onStart(ctx, r)
	}
	return &pyProcess617{RepositoryProcess: r, runner: p.runner}, err
}

type pyProcess617 struct {
	RepositoryProcess
	runner *pyRunner617
}

func (p *pyProcess617) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	out, err := p.RepositoryProcess.Wait(ctx)
	if len(out.Events) > 0 {
		p.runner.mu.Lock()
		copy := out
		copy.Events, copy.Stdout, copy.Stderr = bytes.Clone(out.Events), bytes.Clone(out.Stdout), bytes.Clone(out.Stderr)
		p.runner.eventOutputs = append(p.runner.eventOutputs, copy)
		p.runner.mu.Unlock()
		if p.runner.damage != nil {
			out = p.runner.damage(out)
		}
		if p.runner.afterWait != nil {
			p.runner.afterWait()
		}
	}
	return out, err
}

func fixture617(t *testing.T, image, platform string, cases []pyCase617, extra map[string]string, change func(*repository.Config)) *pyFixture617 {
	t.Helper()
	f := &pyFixture617{root: t.TempDir(), candidate: t.TempDir()}
	config := repository.Config{Version: 1, Parts: []repository.Part{}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	p := &Plan{Name: "exact pytest", Summary: "Return seven from the production API.", Sources: map[string]string{}}
	seen := map[string]bool{}
	for _, c := range cases {
		if !seen[c.part] {
			seen[c.part] = true
			config.Parts = append(config.Parts, part617(c.part, image))
			p.Files = append(p.Files, c.part+"/product.py")
			write468(t, f.root, c.part+"/product.py", "VALUE = 0\n")
		}
		file := c.part + "/" + c.file
		p.Tests = append(p.Tests, Test{Name: "same label", Part: c.part, Framework: "pytest", File: file, Selector: c.file + "::" + c.selector, Says: "Return seven."})
		if previous, ok := p.Sources[file]; ok && previous != c.source {
			t.Fatal("fixture supplied conflicting source")
		}
		p.Sources[file] = c.source
	}
	if change != nil {
		change(&config)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, f.root, repository.Path, string(data))
	for name, source := range extra {
		write468(t, f.root, name, source)
	}
	git468(t, f.root, "init", "-q")
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-qm", "captured pytest base")
	snapshot, err := repository.Load(t.Context(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.plan = n503Read(t, p, snapshot)
	if err := CopyCheckout(t.Context(), f.root, f.candidate); err != nil {
		t.Fatal(err)
	}
	f.runner = &pyRunner617{DockerDependencyRunner: &DockerDependencyRunner{}, t: t}
	f.sandbox = Sandbox{Original: f.root, Timeout: 4 * time.Minute, Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: platform, Runner: f.runner}}}
	t.Cleanup(func() {
		f.runner.mu.Lock()
		defer f.runner.mu.Unlock()
		if f.runner.active != 0 || len(f.runner.opens) != f.runner.closes || f.runner.maxActive > 1 {
			t.Errorf("pytest environments leaked or overlapped: opened=%d closed=%d active=%d max=%d", len(f.runner.opens), f.runner.closes, f.runner.active, f.runner.maxActive)
		}
	})
	return f
}

const pySource617 = "import product\ndef test_ready():\n    assert product.VALUE == 7, 'needs seven'\n"

func control617() []pyCase617 {
	return []pyCase617{{"worker", "test_ready.py", "test_ready", pySource617}}
}
func result617(t *testing.T, r Result, test Test) TestResult {
	t.Helper()
	var found []TestResult
	for _, v := range r.TestResults {
		if v.Phase == "accept" && v.identity() == test.Identity() {
			found = append(found, v)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one result for %s, got %+v", test.Identity(), r.TestResults)
	}
	return found[0]
}
func check617(t *testing.T, f *pyFixture617, eligible bool) *Checked {
	t.Helper()
	ws := n503Draft(t, f.plan)
	// The planner's editable tree is never the independent committed base.
	for _, file := range f.plan.Files {
		write468(t, ws, "repo/"+file, "VALUE = 7\ndef future(): return 7\n")
	}
	c, err := Check(t.Context(), ws, f.root, f.sandbox)
	if err != nil || c == nil || (c.Problem == "") != eligible {
		t.Fatalf("pytest draft qualification want=%v: %v %+v", eligible, err, c)
	}
	if c.Plan == nil || c.Plan.Hash() != f.plan.Hash() {
		t.Fatal("planning lost captured identity or source")
	}
	if eligible {
		for _, test := range f.plan.Tests {
			v := result617(t, c.Run, test)
			if v.Status != "failed" || v.Kind != "test" && v.Kind != "compile" {
				t.Fatalf("qualified without attributable failure: %+v", v)
			}
		}
	}
	return c
}
func fix617(t *testing.T, f *pyFixture617) {
	t.Helper()
	for _, file := range f.plan.Files {
		write468(t, f.candidate, file, "VALUE = 7\ndef future(): return 7\n")
	}
}
func pass617(t *testing.T, f *pyFixture617) Result {
	t.Helper()
	r, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
	if err != nil || !r.Passed() {
		t.Fatalf("production correction needs real pytest passes: %v\n%s", err, r.Summary())
	}
	for _, test := range f.plan.Tests {
		v := result617(t, r, test)
		if v.Status != "passed" || v.Kind != "test" {
			t.Fatalf("candidate lacks normal pytest completion: %+v", v)
		}
	}
	return r
}

func TestPytestOfflineExactResults617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	const parameter = "import pytest, product\n@pytest.mark.parametrize('n', [7, 19], ids=['a b;$(literal).+|x', 'neighbor'])\ndef test_ready(n):\n    assert n == 7\n    assert product.VALUE == n\n"
	cases := []pyCase617{
		{"worker", "one/test_same.py", "test_ready", pySource617},
		{"worker", "two/test_same.py", "test_ready", pySource617},
		{"other", "one/test_same.py", "test_ready", pySource617},
		{"worker", "test_class.py", "TestReady::test_value", "import product\nclass TestReady:\n    def test_value(self):\n        assert product.VALUE == 7\n    def test_neighbor(self):\n        assert False, 'must never run'\n"},
		{"worker", "test_parameter.py", "test_ready[a b;$(literal).+|x]", parameter},
	}
	extra := map[string]string{}
	for _, part := range []string{"worker", "other"} {
		extra[part+"/test_suite.py"] = "import product\ndef test_suite(): assert product.VALUE == 7\n"
		extra[part+"/conftest.py"] = "import pathlib, pytest\n@pytest.fixture(autouse=True)\ndef once(request):\n    path = pathlib.Path('.calls617')\n    seen = path.read_text().splitlines() if path.exists() else []\n    assert request.node.nodeid not in seen, 'selected call ran twice'\n    path.write_text('\\n'.join(seen + [request.node.nodeid]) + '\\n')\n"
	}
	f := fixture617(t, image, platform, cases, extra, func(c *repository.Config) {
		for i := range c.Parts {
			c.Parts[i].Commands["build"] = repository.Command{Argv: []string{"python3", "-c", "import product; assert product.VALUE == 7"}}
			c.Parts[i].Commands["test"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "-q", "test_suite.py"}}
		}
	})
	base := check617(t, f, true)
	for _, part := range []string{"worker", "other"} {
		if status484(t, base.Run, part, "build") != "failed" || status484(t, base.Run, part, "test") != "failed" {
			t.Fatal("ordinary base command failures did not permit independent acceptance")
		}
	}
	for _, v := range base.Run.TestResults {
		if v.Kind != "test" {
			t.Fatalf("assertion was not a test failure: %+v", v)
		}
	}
	fix617(t, f)
	candidate := pass617(t, f)
	for _, part := range []string{"worker", "other"} {
		if status484(t, candidate, part, "build") != "passed" || status484(t, candidate, part, "test") != "passed" {
			t.Fatal("candidate omitted applicable commands")
		}
	}
	for _, test := range f.plan.Tests {
		if _, err := os.Stat(filepath.Join(f.root, test.File)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("draft execution modified original acceptance storage")
		}
	}
	if len(f.runner.eventOutputs) < 2*len(cases) {
		t.Fatal("selectors did not get independent real executions")
	}
	// Every selector must fail on base, even with identical display labels.
	g := fixture617(t, image, platform, []pyCase617{
		{"worker", "test_both.py", "test_fail", "import product\ndef test_fail(): assert product.VALUE == 7\ndef test_pass(): assert True\n"},
		{"worker", "test_both.py", "test_pass", "import product\ndef test_fail(): assert product.VALUE == 7\ndef test_pass(): assert True\n"},
	}, nil, nil)
	c := check617(t, g, false)
	if result617(t, c.Run, g.plan.Tests[0]).Status != "failed" || result617(t, c.Run, g.plan.Tests[1]).Status != "passed" {
		t.Fatal("one selector borrowed a neighbor's outcome")
	}
	c.Run.Output = strings.Repeat("noise", 2000)
	c.Run.Output = c.Run.Tail(16)
	for _, test := range g.plan.Tests {
		if !strings.Contains(c.Feedback(), test.Identity()) {
			t.Fatal("clipping lost full identity")
		}
	}
}

func TestPytestOfflineRefusals617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	control := fixture617(t, image, platform, control617(), nil, nil)
	check617(t, control, true)
	for _, tc := range []struct{ name, source, conftest, selector, setup string }{
		{"skip", "import pytest\ndef test_ready(): pytest.skip('skip')\n", "", "", ""},
		{"xfail", "import pytest\n@pytest.mark.xfail\ndef test_ready(): assert False\n", "", "", ""},
		{"xpass", "import pytest\n@pytest.mark.xfail\ndef test_ready(): assert True\n", "", "", ""},
		{"strict xpass", "import pytest\n@pytest.mark.xfail(strict=True)\ndef test_ready(): assert True\n", "", "", ""},
		{"fixture setup", "import pytest\n@pytest.fixture\ndef broken(): raise RuntimeError('setup')\ndef test_ready(broken): assert False\n", "", "", "setup"},
		{"fixture teardown", "import pytest\n@pytest.fixture\ndef broken():\n    yield\n    raise RuntimeError('teardown')\ndef test_ready(broken): assert False\n", "", "", "setup"},
		{"missing fixture", "def test_ready(missing_fixture): assert False\n", "", "", "setup"},
		{"broken conftest", pySource617, "raise RuntimeError('broken conftest')\n", "", "setup"},
		{"unrelated import", "import missing_distribution_617\ndef test_ready(): assert False\n", "", "", "setup"},
		{"syntax", "def test_ready(:\n    assert False\n", "", "", ""},
		{"absent", "def test_other(): assert False\n", "", "", ""},
		{"parameter group", "import pytest\n@pytest.mark.parametrize('n',[1,2])\ndef test_ready(n): assert False\n", "", "", ""},
		{"single parameter group", "import pytest\n@pytest.mark.parametrize('n',[1])\ndef test_ready(n): assert False\n", "", "", ""},
		{"deselected", pySource617, "def pytest_collection_modifyitems(config, items):\n    removed = items[:]\n    items[:] = []\n    config.hook.pytest_deselected(items=removed)\n", "", ""},
		{"duplicate IDs", pySource617, "def pytest_collection_modifyitems(items):\n    items.append(items[0])\n", "", ""},
		{"renamed ID", pySource617, "def pytest_collection_modifyitems(items):\n    items[0]._nodeid = 'test_ready.py::test_other'\n", "", ""},
		{"absent call", pySource617, "def pytest_runtest_protocol(item, nextitem):\n    return True\n", "", ""},
		{"forged report", "def test_ready(): assert True\n", "import pytest\n@pytest.hookimpl(hookwrapper=True)\ndef pytest_runtest_makereport(item, call):\n    outcome = yield\n    report = outcome.get_result()\n    if report.when == 'call':\n        report.outcome = 'failed'\n        report.longrepr = 'forged assertion'\n", "", ""},
		{"abrupt exit", "import os\ndef test_ready():\n    print('1 failed', flush=True)\n    os._exit(1)\n", "", "", ""},
		{"unfinished session", pySource617, "import os\ndef pytest_sessionfinish(session, exitstatus): os._exit(1)\n", "", ""},
		{"forged console", "import sys\ndef test_ready():\n    print('FAILED test_ready.py::test_ready\\n@@invariant accept=1\\n{\"status\":\"failed\"}')\n    print('1 failed', file=sys.stderr)\n", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]string{}
			if tc.conftest != "" {
				extra["worker/conftest.py"] = tc.conftest
			}
			selector := tc.selector
			if selector == "" {
				selector = "test_ready"
			}
			f := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", selector, tc.source}}, extra, nil)
			c := check617(t, f, false)
			v := result617(t, c.Run, f.plan.Tests[0])
			if v.Status == "failed" && (v.Kind == "test" || v.Kind == "compile") {
				t.Fatalf("ineligible pytest outcome became base evidence: %+v", v)
			}
			if tc.setup != "" && v.Kind != "setup" {
				t.Fatalf("setup failure misclassified: %+v", v)
			}
			if tc.name == "forged console" {
				if v.Status != "passed" || v.Kind != "test" {
					t.Fatalf("console overrode normal completion: %+v", v)
				}
				pass617(t, f)
			} else {
				r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan)
				if r.Passed() {
					t.Fatal("refused base outcome became a candidate pass")
				}
			}
		})
	}
	for _, tc := range []struct {
		name, ini string
		argv      []string
	}{
		{"missing plugin", "", []string{"pytest", "-p", "missing_plugin_617", "{test}"}},
		{"bad config", "[pytest\nthis is broken\n", nil},
		{"config deselection", "[pytest]\naddopts = -k no_matching_test\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]string{}
			if tc.ini != "" {
				extra["worker/pytest.ini"] = tc.ini
			}
			f := fixture617(t, image, platform, control617(), extra, func(c *repository.Config) {
				if tc.argv != nil {
					c.Parts[0].Commands["test_one"] = repository.Command{Argv: tc.argv}
				}
			})
			c := check617(t, f, false)
			v := result617(t, c.Run, f.plan.Tests[0])
			if tc.name != "config deselection" && v.Kind != "setup" {
				t.Fatalf("configuration error misclassified: %+v", v)
			}
		})
	}
	t.Run("missing pytest is setup and is never fetched", func(t *testing.T) {
		f := fixture617(t, image, platform, control617(), nil, func(c *repository.Config) {
			// Remove only the disposable container's copy of pytest, after the
			// pinned image was checked. This is a real missing-framework failure.
			c.Parts[0].Commands["build"] = repository.Command{Argv: []string{"python3", "-c", "import pathlib, pytest, shutil; shutil.rmtree(pathlib.Path(pytest.__file__).parent); import _pytest; shutil.rmtree(pathlib.Path(_pytest.__file__).parent)"}}
		})
		c := check617(t, f, false)
		if v := result617(t, c.Run, f.plan.Tests[0]); v.Kind != "setup" {
			t.Fatalf("missing pytest became a failure about the test: %+v", v)
		}
	})
}

func TestPytestOfflineMissingAPI617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	for _, method := range []bool{false, true} {
		t.Run(fmt.Sprint("method=", method), func(t *testing.T) {
			source, selector := "from product import future\ndef test_ready():\n    assert future() == 7\n", "test_ready"
			if method {
				source, selector = "from product import future\nclass TestReady:\n    def test_ready(self):\n        assert future() == 7\n", "TestReady::test_ready"
			}
			f := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", selector, source}}, nil, nil)
			c := check617(t, f, true)
			v := result617(t, c.Run, f.plan.Tests[0])
			if v.Kind != "compile" || v.Evidence == nil || !v.Evidence.Complete || v.Evidence.ExitCode <= 0 || !strings.Contains(v.Reason, "future") {
				t.Fatalf("missing API lacks structured completed attribution: %+v", v)
			}
			for _, record := range v.Evidence.Records {
				b := record.Binding
				if b.InvocationID == "" || b.Part != "worker" || b.Framework != "pytest" || b.File != f.plan.Tests[0].File || b.Selector != f.plan.Tests[0].Selector || b.SourceDigest != dependencyHash([]byte(source)) || b.ExecutionDigest != f.plan.Execution.Digest {
					t.Fatalf("attribution lost binding: %+v", b)
				}
			}
			fix617(t, f)
			pass617(t, f)
		})
	}
	for _, tc := range []struct{ name, source, product string }{
		{"unused import", "from product import future\ndef test_ready(): assert False\n", "VALUE=0\n"},
		{"neighbor API", "from product import future\ndef test_ready(): assert False\ndef test_neighbor(): assert future()==7\n", "VALUE=0\n"},
		{"broken production dependency", "from product import future\ndef test_ready(): assert future()==7\n", "import missing_distribution_617\n"},
		{"raised ImportError", "from product import future\ndef test_ready(): assert future()==7\n", "raise ImportError(\"cannot import name 'future'\")\n"},
		{"missing distribution", "from missing_distribution_617 import future\ndef test_ready(): assert future()==7\n", "VALUE=0\n"},
		{"helper ambiguity", "from product import future\ndef helper(): return future()\ndef test_ready(): assert helper()==7\n", "VALUE=0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", "test_ready", tc.source}}, map[string]string{"worker/product.py": tc.product}, nil)
			c := check617(t, f, false)
			v := result617(t, c.Run, f.plan.Tests[0])
			if v.Status == "failed" && (v.Kind == "compile" || v.Kind == "test") {
				t.Fatalf("unattributed import qualified: %+v", v)
			}
		})
	}
}

func TestPytestOfflineAuthority617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	f := fixture617(t, image, platform, control617(), nil, nil)
	check617(t, f, true)
	if len(f.runner.eventOutputs) == 0 {
		t.Fatal("real framework supplied no private events")
	}
	prior := bytes.Clone(f.runner.eventOutputs[len(f.runner.eventOutputs)-1].Events)
	for _, mode := range []string{"replay", "duplicate", "truncate", "stdout only", "not drained", "missing process start"} {
		t.Run(mode, func(t *testing.T) {
			g := fixture617(t, image, platform, control617(), nil, nil)
			g.runner.damage = func(out RepositoryProcessOutput) RepositoryProcessOutput {
				switch mode {
				case "replay":
					out.Events = bytes.Clone(prior)
				case "duplicate":
					out.Events = append(bytes.Clone(out.Events), out.Events...)
				case "truncate":
					out.Events = out.Events[:len(out.Events)/2]
				case "stdout only":
					out.Stdout = append(out.Stdout, out.Events...)
					out.Events = nil
				case "not drained":
					out.Drained = false
				case "missing process start":
					out.Started = false
				}
				return out
			}
			check617(t, g, false)
			fix617(t, g)
			r, _ := g.sandbox.Run(t.Context(), g.candidate, g.plan)
			if r.Passed() {
				t.Fatal("corrupted evidence qualified candidate")
			}
		})
	}
	fix617(t, f)
	write468(t, f.candidate, repository.Path, `{"version":999,"parts":[]}`)
	pass617(t, f)
	for _, mode := range []string{"source", "execution", "identity", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			g := fixture617(t, image, platform, control617(), nil, nil)
			fix617(t, g)
			switch mode {
			case "source":
				write468(t, g.candidate, g.plan.Tests[0].File, "def test_ready(): assert True\n")
			case "execution":
				g.plan.Execution.Digest = hash468("forged policy")
			case "identity":
				g.plan.Tests[0].Selector = "test_ready.py::test_other"
			case "symlink":
				if err := os.Symlink("product.py", filepath.Join(g.candidate, g.plan.Tests[0].File)); err != nil {
					t.Fatal(err)
				}
			}
			r, _ := g.sandbox.Run(t.Context(), g.candidate, g.plan)
			if r.Passed() || len(g.runner.opens) != 0 {
				t.Fatal("tampered authority reached execution")
			}
		})
	}
	t.Run("source changed during collection", func(t *testing.T) {
		g := fixture617(t, image, platform, control617(), map[string]string{"worker/conftest.py": "def pytest_collection_modifyitems(items):\n    path = items[0].path\n    path.write_text(path.read_text() + '\\n# altered during collection\\n')\n"}, nil)
		check617(t, g, false)
		fix617(t, g)
		r, _ := g.sandbox.Run(t.Context(), g.candidate, g.plan)
		if r.Passed() {
			t.Fatal("mutated runtime source passed")
		}
	})
}

func TestPytestOfflineLifetime617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	f := fixture617(t, image, platform, []pyCase617{{"worker", "test_one.py", "test_ready", pySource617}, {"worker", "test_two.py", "test_ready", pySource617}, {"other", "test_one.py", "test_ready", pySource617}}, nil, nil)
	check617(t, f, true)
	if len(f.runner.deadlines) < 3 {
		t.Fatal("real selectors did not share a test phase")
	}
	for _, deadline := range f.runner.deadlines[1:] {
		if !deadline.Equal(f.runner.deadlines[0]) {
			t.Fatal("a later selector or part renewed the test allowance")
		}
	}
	for _, mode := range []string{"canceled after failure", "timeout after failure", "cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			g := fixture617(t, image, platform, control617(), nil, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled after failure" {
				g.runner.afterWait = cancel
			}
			if mode == "timeout after failure" {
				// Wait until the inherited phase expires after genuine observations.
				g.sandbox.ShardTimeout = 3 * time.Second
				g.runner.afterWait = func() {
					end := g.runner.deadlines[0]
					if d := time.Until(end); d > 0 {
						time.Sleep(d + 10*time.Millisecond)
					}
				}
			}
			if mode == "cleanup failure" {
				g.runner.afterClose = func(context.Context) error { return errors.New("injected cleanup failure") }
			}
			c, _ := Check(ctx, n503Draft(t, g.plan), g.root, g.sandbox)
			if c == nil || c.Problem == "" || c.Run.Passed() {
				t.Fatal("lifetime failure failed to veto real evidence")
			}
			if mode == "canceled after failure" && !c.Run.Interrupted {
				t.Fatal("cancellation was lost")
			}
			if mode == "timeout after failure" && !c.Run.TimedOut && !c.Run.DeadlineExceeded {
				t.Fatal("phase timeout was lost")
			}
		})
	}
	t.Run("cancel active pytest and its child", func(t *testing.T) {
		source := "import subprocess, sys, time\ndef test_ready():\n    subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(600)'])\n    print('live-child-617', flush=True)\n    time.sleep(600)\n"
		g := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", "test_ready", source}}, nil, func(c *repository.Config) {
			c.Parts[0].Commands["test_one"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "-s", "{test}"}}
		})
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		observed := make(chan bool, 8)
		g.runner.onStart = func(processCtx context.Context, p RepositoryProcess) {
			go func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if strings.Contains(string(p.Snapshot().Stdout), "live-child-617") {
						observed <- true
						cancel()
						return
					}
					select {
					case <-processCtx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
		}
		c, _ := Check(ctx, n503Draft(t, g.plan), g.root, g.sandbox)
		if c == nil || c.Problem == "" || !c.Run.Interrupted {
			t.Fatal("active cancellation supplied completion")
		}
		select {
		case <-observed:
		default:
			t.Fatal("real pytest never reached its live child")
		}
		for _, q := range g.runner.opens {
			cleanup, stop := context.WithTimeout(t.Context(), 5*time.Second)
			_, err := dependencyDocker(cleanup, "inspect", q.Token)
			stop()
			if err == nil {
				t.Fatal("cancellation left the workload container or its child alive")
			}
		}
	})
	first := fixture617(t, image, platform, control617(), nil, nil)
	second := fixture617(t, image, platform, control617(), nil, nil)
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	cleaning, release, waiting := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, releaseOnce, waitOnce sync.Once
	first.runner.afterClose = func(ctx context.Context) error {
		once.Do(func() { close(cleaning) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a, finishA, err := q.RegisterPlan(t.Context(), first.sandbox, first.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finishA)
	b, finishB, err := q.RegisterPlan(WithCheckStatus(t.Context(), func(wait bool) {
		if wait {
			waitOnce.Do(func() { close(waiting) })
		}
	}), second.sandbox, second.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finishB)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	wsA, wsB := n503Draft(t, first.plan), n503Draft(t, second.plan)
	doneA := make(chan *Checked, 1)
	go func() { c, _ := a.check(t.Context(), wsA); doneA <- c }()
	select {
	case <-cleaning:
	case <-time.After(time.Minute):
		t.Fatal("first check did not reach cleanup")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	doneB := make(chan error, 1)
	go func() { _, err := b.check(ctx, wsB); doneB <- err }()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("queue released slot before cleanup")
	}
	cancel()
	select {
	case err := <-doneB:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting cancellation blocked")
	}
	finishB()
	if len(second.runner.opens) != 0 {
		t.Fatal("queued check started before cleanup")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case c := <-doneA:
		if c == nil || c.Problem != "" {
			t.Fatalf("cleanup lost genuine failure: %+v", c)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cleanup failed to release slot")
	}
}
