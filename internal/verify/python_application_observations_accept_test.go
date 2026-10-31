package verify

// Issue #318 acceptance contract. This extends the #315 session API; it does
// not connect public verification, TLC, report attribution, synthesis or proof.
//
// Gate-owned Python interface, available only in the staged trusted runtime:
//
//   from invariant_observe import functional, instance
//   with functional(advance, application.start, argument="state", field="task",
//                   expected_initial={"task": "pending"}) as lifecycle:
//       lifecycle.step(application.tick, expected_before={"task": "pending"},
//                      expected_after={"task": "running"})
//
// functional supports an undecorated synchronous module function whose named
// state argument and returned state are immutable JSON scalars (str, bool,
// finite number, None). The gate calls the staged application initializer and
// reads its return, owns the current state and passes it as the first argument
// to the application entry point on step. It observes exactly one selected
// target call inside that entry point, binds its actual state argument to the
// current state, reads the actual return and checks that the entry point returns
// that result. It advances the lifecycle from this read-back, not from expected
// dictionaries. State is value-semantic; no mutable-object identity is claimed
// for this form. Initializer/entry functions must be staged application code,
// outside the driver, and the entry must be distinct from the selected target.
//
//   with instance(task.advance, fields={"task": "state"},
//                 expected_initial={"task": "pending"}) as lifecycle:
//       lifecycle.step(application.tick, expected_before={"task": "pending"},
//                      expected_after={"task": "running"})
//
// instance supports ordinary undecorated synchronous Python instance methods
// on ordinary instances, reading named scalar fields directly from the same
// receiver's own __dict__. Gate code reads initial state and the receiver at
// call entry/return; the observed frame's receiver MUST be that exact object.
// fields is a declarative state-key-to-attribute mapping, never a callback.
// Properties, descriptors, custom attribute access, slots, closures, wrapped or
// replaced callables, async/generators, mutable/non-JSON state, concurrency and
// reentrant/nested selected calls are refused with a diagnostic. Do not guess
// bindings or silently unwrap unsupported forms. Driver/target/entry/initializer
// identities must be validated against staged bytes and lexical declarations,
// not solely __name__, __qualname__, co_filename or claimed source hashes.
//
// Both forms are context managers. A context allocates a new monotonically
// numbered run, with an execution-local instance identity (a functional handle
// is its own state instance). It must complete at least one step; a step must
// execute exactly one target call while the configured driver and the supplied
// application entry point execute. Initial/pre/post expected dictionaries are
// optional assertions, never evidence or overrides. The gate reads them itself
// and checks continuity with the previous post-state before accepting a step.
// An exception, mismatch, reused/closed handle or unsupported binding poisons
// execution even if the driver catches it. Contexts and instances may coexist;
// their ordered observations cannot cross. No application source is rewritten.
// Effective runs/steps are maximum sampling budgets, seed is an actual runner
// setting; record them apart from declarations. Exceeding either budget fails.
// Defaults remain 1000/40/1. All success is sampled, including imported legacy
// harnesses. Execute the configured script as __main__ with literal ordered
// argv, from its declared cwd; do not silently call an unexecuted drive().
//
// Extend PythonApplicationExecutionResult's JSON with observation_mode
// ("sampled" for successful conformance) and observations (ordered records).
// Each record has kind (initial/transition), run, instance, sequence (initial
// 0, steps 1..), key (full invocation key), target and driver (actual lexical
// IDs), source_sha256, driver_sha256, symbol, entry, entry_sha256, and readback
// (state-key -> "$return" for functional, attribute name for instance).
// Initial has state; transition has before/after. Entry identifies the actual
// initializer for functional initial, the target for instance initial, and
// the actual surrounding application entry for transitions. Every transition
// establishes an actual selected call, including exact staged source/symbol
// and same receiver/state. This raw evidence carries no proof, TLC verdict,
// exhaustive-state or attempted-step coverage and invents no assertions.
// A conformance success requires complete valid nonempty evidence and exit 0.
// Its Tests is empty: independently executed test results retain all their
// existing outcomes and cannot be promoted by a driver. Invalid conformance
// execution returns no accepted Observations, Success=false and diagnostics.
//
// Freshness: use per-execution transport identity, independently validated in
// Go, in addition to portable snapshot/runner/key binding. Replayed output from
// the same session/key must fail. Transient identifiers never enter portable
// JSON/fingerprints. Bind all runner/collector assets, the complete staged tree
// and dependency evidence. Preserve the existing before/after/reuse snapshot
// checks, detached views, image reuse, shutdown ownership and cancellation.
// Preserve offline execution, limits, absolute interpreter, effective seed 0,
// trusted startup, namespace precedence, proxy suppression and early refusal
// of unsupported commands/proof/environment overrides. Never install on Run.
//
// Extend builder-owned TestPythonApplicationExecution* real-Docker regressions
// to exercise EVERY positive/negative form here, recursive requirements and
// wheel evidence, hostile namespaces/startup/scripts, unavailable image,
// cancellation cleanup and resource limits. Retain #315's full regressions,
// public guards, model-only, supplied attribution, legacy/marked Python and TS.
// Only the superseded conformance-unsupported expectation/comments in the
// named existing #315 acceptance file change; all its other checks remain.
// README and SPEC must document this complete direct adapter, these examples,
// supported forms/refusals, raw sampled results, freshness and session reuse.
// Changed SPEC bullets cite D-0013,D-0029,D-0047,D-0054,D-0158,D-0168,D-0170,
// D-0171,D-0177,D-0178,D-0180,D-0122 as appropriate, plus the preceding PR's
// session-policy decision when recorded. The baseline has no such decision;
// do not invent its ID. Link #313's child plan:
// https://github.com/gitdek/invariant/issues/313#issuecomment-6536681769
// sha256:0380e0c90425c12cb24fed241112820b07758fecb6d812130b71424a857d2366.
// Merger work: record the session (if still missing) and observation/execution
// decisions/dependencies with decision tooling and run the explicitly enabled
// Docker suite on final head, posting passing output. No decisions/, pinned
// statements or ratified locks may be edited by this build. Docker/network
// unavailability and decision recording alone do not require repair.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

const pa318App = `from tasks.lifecycle import advance
def start():
    return 'pending'
def tick(state):
    return advance(state)
class Task:
    def __init__(self):
        self.state = 'pending'
    def advance(self):
        self.state = advance(self.state)
class Application:
    def __init__(self, task):
        self.task = task
    def tick(self):
        self.task.advance()
def unused(state):
    return 'running'
def wrong(state):
    return other(state)
def other(state):
    return 'running'
def wrong_input(state):
    return advance('other')
def wrong_return(state):
    advance(state)
    return 'substitute'
`

const pa318Functional = `from invariant_observe import functional
from tasks.lifecycle import advance
from tasks import application
def drive():
    with functional(advance, application.start, argument='state', field='task', expected_initial={'task':'pending'}) as life:
        life.step(application.tick, expected_before={'task':'pending'}, expected_after={'task':'running'})
        life.step(application.tick, expected_before={'task':'running'}, expected_after={'task':'running'})
if __name__ == '__main__':
    drive()
`

const pa318Instance = `from invariant_observe import instance
from tasks.application import Task, Application
def drive():
    first, second = Task(), Task()
    with instance(first.advance, fields={'task':'state'}, expected_initial={'task':'pending'}) as a:
        with instance(second.advance, fields={'task':'state'}, expected_initial={'task':'pending'}) as b:
            a.step(Application(first).tick, expected_before={'task':'pending'}, expected_after={'task':'running'})
            b.step(Application(second).tick, expected_before={'task':'pending'}, expected_after={'task':'running'})
            a.step(Application(first).tick, expected_before={'task':'running'}, expected_after={'task':'running'})
if __name__ == '__main__':
    drive()
`

type pa318Observation struct {
	Kind         string                         `json:"kind"`
	Run          int                            `json:"run"`
	Instance     int                            `json:"instance"`
	Sequence     int                            `json:"sequence"`
	Key          PythonApplicationInvocationKey `json:"key"`
	Target       string                         `json:"target"`
	Driver       string                         `json:"driver"`
	SourceSHA256 string                         `json:"source_sha256"`
	DriverSHA256 string                         `json:"driver_sha256"`
	Symbol       string                         `json:"symbol"`
	Entry        string                         `json:"entry"`
	EntrySHA256  string                         `json:"entry_sha256"`
	Readback     map[string]string              `json:"readback"`
	State        map[string]any                 `json:"state"`
	Before       map[string]any                 `json:"before"`
	After        map[string]any                 `json:"after"`
}

func pa318Fixture(t *testing.T, wheel bool, driver string, method bool) (string, *project.Project) {
	t.Helper()
	root, p := pa315Fixture(t, wheel)
	// The preceding fixture's lifecycle, sibling, resource and tests stay exact.
	// This is ordinary surrounding application code, with no instrumentation.
	pa310Write(t, root, "tasks/application.py", pa318App)
	pa310Write(t, root, pa315Project+"/driver.py", driver)
	if method {
		p.Manifest.Components.Checks[0].Target = pa310Ref("tasks/application.py", "Task.advance")
	}
	p.Manifest.Components.Checks[0].Invocations[1].Sampling = map[string]string{"runs": "3", "steps": "4", "seed": "7"}
	pa310Save(t, p)
	return root, p
}

func pa318Driver(t *testing.T, s *PythonApplicationSession) PythonApplicationInvocation {
	t.Helper()
	for _, v := range s.Invocations() {
		if v.Key.Kind == "conformance" {
			return v
		}
	}
	t.Fatal("prepared driver missing")
	return PythonApplicationInvocation{}
}

// Execute the production Python runner, not a protocol double. Only mounts,
// interpreter path and library location are translated for Docker-free tests.
// -P is removed for host Python 3.9/3.10 compatibility; the opt-in suite uses
// the exact pinned production interpreter and startup flags without changes.
func pa318Local(t *testing.T) func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	libraries := t.TempDir()
	return func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		b, err := os.ReadFile(filepath.Join(c.RuntimeDir, "request.json"))
		if err != nil {
			return PythonApplicationProcess{}, err
		}
		var req map[string]any
		if err = json.Unmarshal(b, &req); err != nil {
			return PythonApplicationProcess{}, err
		}
		req["root"], req["output"], req["libraries"] = c.Root, filepath.Join(c.OutputDir, "events.jsonl"), libraries
		b, err = json.Marshal(req)
		if err != nil {
			return PythonApplicationProcess{}, err
		}
		request := filepath.Join(c.OutputDir, "host-request.json")
		if err = os.WriteFile(request, b, 0600); err != nil {
			return PythonApplicationProcess{}, err
		}
		var args []string
		for _, a := range c.Args {
			if a == "-P" {
				continue
			}
			if a == "/runtime/runner.py" {
				a = filepath.Join(c.RuntimeDir, "runner.py")
			}
			if a == "/runtime/request.json" {
				a = request
			}
			args = append(args, a)
		}
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = filepath.Join(c.Root, strings.TrimPrefix(c.WorkingDir, "/src"))
		for k, v := range c.Env {
			if k == "APP_EVENTS" && v == "/output/events.jsonl" {
				v = filepath.Join(c.OutputDir, "events.jsonl")
			}
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		p := PythonApplicationProcess{ExitCode: -1, Stdout: stdout.String(), Stderr: stderr.String()}
		if cmd.ProcessState != nil {
			p.ExitCode = cmd.ProcessState.ExitCode()
		}
		p.Events, _ = os.ReadFile(filepath.Join(c.OutputDir, "events.jsonl"))
		return p, err
	}
}

func pa318Options(t *testing.T, real bool, selected *int) PythonApplicationSessionOptions {
	t.Helper()
	if real {
		return PythonApplicationSessionOptions{SelectImage: func(ctx context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
			*selected++
			return selectPythonApplicationImage(ctx, w)
		}}
	}
	o := pa315Options(selected)
	o.Execute = pa318Local(t)
	return o
}

func pa318Rows(t *testing.T, r *PythonApplicationExecutionResult) []pa318Observation {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Mode string             `json:"observation_mode"`
		Rows []pa318Observation `json:"observations"`
	}
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	if r.Success && (view.Mode != "sampled" || len(view.Rows) == 0) {
		t.Fatalf("no sampled observations: %s", b)
	}
	return view.Rows
}

func pa318Success(t *testing.T, ctx context.Context, s *PythonApplicationSession) *PythonApplicationExecutionResult {
	t.Helper()
	r, err := s.Run(ctx, pa318Driver(t, s).Key)
	if err != nil || r == nil || !r.Success || !r.Complete || r.ExitCode != 0 {
		t.Fatalf("configured driver did not produce bound observations: result=%+v error=%v", r, err)
	}
	if len(r.Tests) != 0 {
		t.Fatalf("driver invented runtime test records: %+v", r.Tests)
	}
	pa318Rows(t, r)
	return r
}

func pa318CheckRows(t *testing.T, r *PythonApplicationExecutionResult, before map[string]string, method bool) {
	t.Helper()
	rows := pa318Rows(t, r)
	want := 3
	if method {
		want = 5
	}
	if len(rows) != want {
		t.Fatalf("got %d observations, want %d: %+v", len(rows), want, rows)
	}
	target, symbol := pa315Target, "advance"
	if method {
		target, symbol = "tasks/application.py", "Task.advance"
	}
	states := map[int]map[string]any{}
	instances := map[int]int{}
	sequences := map[int]int{}
	for _, row := range rows {
		if row.Key != r.Key || row.Target != r.Key.Target || row.Driver != r.Key.Check || row.Symbol != symbol || row.SourceSHA256 != before[target] || row.DriverSHA256 != before[pa315Project+"/driver.py"] || row.Run < 1 || row.Instance < 1 {
			t.Fatalf("unbound observation: %+v", row)
		}
		field := "$return"
		if method {
			field = "state"
		}
		if !reflect.DeepEqual(row.Readback, map[string]string{"task": field}) || row.EntrySHA256 != before["tasks/application.py"] {
			t.Fatalf("read-back/entry binding missing: %+v", row)
		}
		switch row.Kind {
		case "initial":
			if states[row.Run] != nil || row.Sequence != 0 || !reflect.DeepEqual(row.State, map[string]any{"task": "pending"}) {
				t.Fatalf("bad initial: %+v", row)
			}
			entry := "start"
			if method {
				entry = "Task.advance"
			}
			if row.Entry != pa310ID("tasks/application.py", entry) {
				t.Fatalf("wrong initial reader: %+v", row)
			}
			states[row.Run], instances[row.Run] = row.State, row.Instance
		case "transition":
			entry := "tick"
			if method {
				entry = "Application.tick"
			}
			if row.Entry != pa310ID("tasks/application.py", entry) || row.Instance != instances[row.Run] || row.Sequence != sequences[row.Run]+1 || !reflect.DeepEqual(row.Before, states[row.Run]) || !reflect.DeepEqual(row.After, map[string]any{"task": "running"}) {
				t.Fatalf("call/read-back continuity broken: %+v", row)
			}
			states[row.Run], sequences[row.Run] = row.After, row.Sequence
		default:
			t.Fatalf("unknown observation: %+v", row)
		}
	}
	if method && (len(states) != 2 || instances[1] == instances[2]) {
		t.Fatalf("instances conflated: %+v", rows)
	}
}

func pa318Positive(t *testing.T, real bool, wheel bool, method bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	driver := pa318Functional
	if method {
		driver = pa318Instance
	}
	if wheel {
		driver = strings.Replace(driver, "def drive():", "def drive():\n    from packaging.version import Version\n    assert Version('1.9') < Version('1.10')", 1)
	}
	root, p := pa318Fixture(t, wheel, driver, method)
	before := pa315Tree(t, root)
	selected := 0
	opts := pa318Options(t, real, &selected)
	execute := opts.Execute
	if execute == nil {
		execute = RunPythonApplicationCommand
	}
	var commands []PythonApplicationCommand
	opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		commands = append(commands, c)
		return execute(ctx, c)
	}
	s := pa315Prepare(t, p, opts)
	first := pa318Success(t, ctx, s)
	pa318CheckRows(t, first, before, method)
	second := pa318Success(t, ctx, s)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) || first.Fingerprint == "" || first.Fingerprint != second.Fingerprint {
		t.Fatalf("repeat evidence differs:\n%s\n%s", a, b)
	}
	if selected != 1 || len(commands) != 2 || commands[0].Image != commands[1].Image {
		t.Fatal("execution reselected dependencies")
	}
	if !reflect.DeepEqual(pa315Files(first.Provenance), before) || first.Actual.LauncherSHA256 != s.Provenance().RunnerSHA256 {
		t.Fatal("incomplete staged/runtime provenance")
	}
	if first.Declared.Command != "python" || first.Actual.Interpreter != "/usr/local/bin/python" || first.Actual.WorkingDir != "/src/"+pa315Project || first.Actual.Env["PYTHONHASHSEED"] != "0" || !reflect.DeepEqual(first.Actual.Sampling, map[string]string{"runs": "3", "steps": "4", "seed": "7"}) {
		t.Fatalf("lost declared/actual controls: %+v", first)
	}
	wantArgs := pa318Driver(t, s).Declared.Args
	if len(first.Actual.Args) < len(wantArgs) || !reflect.DeepEqual(first.Actual.Args[len(first.Actual.Args)-len(wantArgs):], wantArgs) {
		t.Fatal("driver argv changed")
	}
	for _, host := range []string{root, commands[0].Root, commands[0].RuntimeDir, commands[0].OutputDir} {
		if strings.Contains(string(a), host) {
			t.Fatalf("portable evidence leaks %q", host)
		}
	}
	if wheel {
		d := first.Provenance.Dependencies
		if d == nil || d.Image != commands[0].Image || d.Base != toolchain.PythonImage || d.Platform != "linux/amd64" || d.Recipe == "" || len(d.Inputs) != 3 || len(d.CanonicalInputs) != 2 || len(d.Artifacts) != 1 || d.Artifacts[0].Name != "packaging" || d.Artifacts[0].Version != "24.2" || d.Artifacts[0].SHA256 == "" {
			t.Fatalf("wheel evidence missing: %+v", d)
		}
		for _, in := range d.Inputs {
			if before[in.Path] != in.SHA256 {
				t.Fatal("wrong dependency input hash")
			}
		}
	} else if first.Provenance.Dependencies != nil {
		t.Fatal("invented dependencies")
	}
	_, q := pa318Fixture(t, wheel, driver, method)
	relocated := pa315Prepare(t, q, pa318Options(t, real, new(int)))
	third := pa318Success(t, ctx, relocated)
	c, _ := json.Marshal(third)
	if !bytes.Equal(a, c) {
		t.Fatalf("relocation evidence differs:\n%s\n%s", a, c)
	}
	if !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("execution changed original bytes")
	}
}

func TestPythonApplicationExecutionBoundObservations(t *testing.T) {
	for _, method := range []bool{false, true} {
		t.Run(fmt.Sprintf("method=%v", method), func(t *testing.T) { pa318Positive(t, false, false, method) })
	}
}

type pa318BadDriver struct {
	name, driver, app, target string
	method                    bool
}

func pa318BadDrivers() []pa318BadDriver {
	replace := func(from, to string) string { return strings.Replace(pa318Functional, from, to, 1) }
	method := func(from, to string) string { return strings.Replace(pa318Instance, from, to, 1) }
	cases := []pa318BadDriver{
		{name: "unused", driver: "def drive():\n    pass\nif __name__ == '__main__': drive()\n"},
		{name: "import only", driver: "from tasks.lifecycle import advance\ndef drive():\n    pass\nif __name__ == '__main__': drive()\n"},
		{name: "configured driver never called", driver: replace("    drive()", "    pass")},
		{name: "empty lifecycle", driver: replace("        life.step(application.tick, expected_before={'task':'pending'}, expected_after={'task':'running'})\n        life.step(application.tick, expected_before={'task':'running'}, expected_after={'task':'running'})", "        pass")},
		{name: "no selected call", driver: replace("life.step(application.tick", "life.step(application.unused")},
		{name: "wrong same-file symbol", driver: replace("life.step(application.tick", "life.step(application.wrong"), app: strings.Replace(pa318App, "return other(state)", "from tasks.lifecycle import other\n    return other(state)", 1), target: "from .helper import next_state\nfrom pathlib import Path\ndef advance(state):\n    return next_state(state, Path(__file__).with_name('next.txt').read_text().strip())\ndef other(state):\n    return 'running'\n"},
		{name: "replacement before binding", driver: "import tasks.lifecycle\ntasks.lifecycle.advance = lambda state: 'running'\n" + pa318Functional},
		{name: "replacement after binding", driver: replace("        life.step(application.tick", "        application.advance = lambda state: 'running'\n        life.step(application.tick")},
		{name: "wrong initial", driver: replace("expected_initial={'task':'pending'}", "expected_initial={'task':'running'}")},
		{name: "wrong pre-state", driver: replace("expected_before={'task':'pending'}", "expected_before={'task':'running'}")},
		{name: "wrong post-state", driver: replace("expected_after={'task':'running'}", "expected_after={'task':'done'}")},
		{name: "wrong selected argument", driver: replace("life.step(application.tick", "life.step(application.wrong_input")},
		{name: "touch target return substitute", driver: replace("life.step(application.tick", "life.step(application.wrong_return")},
		{name: "direct target is not surrounding application", driver: replace("life.step(application.tick", "life.step(advance")},
		{name: "driver initializer", driver: replace("def drive():", "def fake_initial():\n    return 'pending'\ndef drive():"), app: pa318App},
		{name: "import failure", driver: "import missing_pa318_application_module\n" + pa318Functional},
		{name: "runtime failure", driver: replace("    drive()", "    drive()\n    raise RuntimeError('driver runtime failed')")},
		{name: "early exit zero", driver: replace("    drive()", "    import os\n    os._exit(0)")},
		{name: "untrusted JSON trace only", driver: "import json\nfrom tasks.lifecycle import advance\ndef drive():\n    advance('pending')\n    print(json.dumps({'target':'advance','initial':{'task':'pending'},'after':{'task':'running'}}))\nif __name__ == '__main__': drive()\n"},
		{name: "cross instance", method: true, driver: method("a.step(Application(first).tick", "a.step(Application(second).tick")},
		{name: "touched real target substitute lifecycle", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "        self.task.advance()", "        Task().advance()\n        self.task.state = 'running'", 1)},
		{name: "mutated between transitions", method: true, driver: method("            a.step(Application(first).tick, expected_before={'task':'running'}", "            first.state = 'pending'\n            a.step(Application(first).tick, expected_before={'task':'running'}")},
		{name: "wrong instance initial", method: true, driver: method("expected_initial={'task':'pending'}", "expected_initial={'task':'running'}")},
		{name: "wrong instance transition", method: true, driver: method("expected_after={'task':'running'}", "expected_after={'task':'substitute'}")},
		{name: "closed run reused", driver: replace("if __name__ == '__main__':", "    life.step(application.tick, expected_before={'task':'running'}, expected_after={'task':'running'})\nif __name__ == '__main__':")},
		{name: "multiple selected calls", driver: pa318Functional, app: strings.Replace(pa318App, "    return advance(state)", "    advance(state)\n    return advance(state)", 1)},
		{name: "mutable functional state", driver: pa318Functional, app: strings.Replace(pa318App, "return 'pending'", "return {'task':'pending'}", 1)},
		{name: "property read-back", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "        self.state = 'pending'", "        self._state = 'pending'\n    @property\n    def state(self):\n        return self._state\n    @state.setter\n    def state(self, value):\n        self._state = value", 1)},
		{name: "custom attribute read-back", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "class Task:", "class Task:\n    def __getattribute__(self, name):\n        return object.__getattribute__(self, name)", 1)},
		{name: "caught mismatch", driver: replace("    drive()", "    try:\n        drive()\n    except Exception:\n        pass")},
	}
	for i := range cases {
		if cases[i].name == "driver initializer" {
			cases[i].driver = strings.Replace(cases[i].driver, "functional(advance, application.start", "functional(advance, fake_initial", 1)
		}
		if cases[i].name == "caught mismatch" {
			cases[i].driver = strings.Replace(cases[i].driver, "expected_initial={'task':'pending'}", "expected_initial={'task':'bogus'}", 1)
		}
	}
	for _, kind := range []string{"missing", "empty", "malformed"} {
		action := "os.unlink(os.environ['APP_EVENTS'])"
		if kind == "empty" {
			action = "open(os.environ['APP_EVENTS'], 'w').close()"
		}
		if kind == "malformed" {
			action = "open(os.environ['APP_EVENTS'], 'w').write('{broken}\\n')"
		}
		cases = append(cases, pa318BadDriver{name: kind + " output", driver: replace("    drive()", "    drive()\n    import os\n    "+action+"\n    os._exit(0)")})
	}
	return cases
}

func pa318Reject(t *testing.T, r *PythonApplicationExecutionResult, err error) {
	t.Helper()
	if r != nil && (r.Success || len(pa318Rows(t, r)) != 0) {
		t.Fatalf("invalid driver yielded accepted observations: %+v error=%v", r, err)
	}
	if err == nil && (r == nil || len(r.Diagnostics) == 0) {
		t.Fatalf("failed execution has no diagnostic: %+v", r)
	}
}

func pa318Negative(t *testing.T, real bool) {
	t.Helper()
	for _, tc := range pa318BadDrivers() {
		t.Run(tc.name, func(t *testing.T) {
			root, p := pa318Fixture(t, false, tc.driver, tc.method)
			if tc.app != "" {
				pa310Write(t, root, "tasks/application.py", tc.app)
			}
			if tc.target != "" {
				pa310Write(t, root, pa315Target, tc.target)
			}
			p.Manifest.Components.Checks[0].Invocations[1].Env = map[string]string{"APP_EVENTS": "/output/events.jsonl"}
			pa310Save(t, p)
			before := pa315Tree(t, root)
			selected := 0
			s := pa315Prepare(t, p, pa318Options(t, real, &selected))
			r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("failure changed originals or image selection")
			}
		})
	}
}

func TestPythonApplicationExecutionRejectsUnboundObservations(t *testing.T) {
	// A rejection-only test would already pass the old blanket refusal.
	pa318Positive(t, false, false, false)
	pa318Negative(t, false)
}

func pa318RuntimeIndependence(t *testing.T, real bool) {
	t.Helper()
	for _, tc := range []struct{ symbol, selection, outcome string }{
		{"test_pass", "test_pass", "passed"},
		{"test_fail", "test_fail", "failed"},
		{"test_skip", "test_skip", "skipped"},
		{"test_error", "test_error", "error"},
		{"test_fail", "test_pass", "not_run"},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			root, p := pa318Fixture(t, false, pa318Functional, false)
			pa315Select(t, p, "TaskTests."+tc.symbol, "tests.test_tasks.TaskTests."+tc.selection)
			before := pa315Tree(t, root)
			selected := 0
			s := pa315Prepare(t, p, pa318Options(t, real, &selected))
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if r == nil {
				t.Fatalf("runtime result missing: %v", err)
			}
			found := false
			for _, row := range r.Tests {
				if row.Check == pa315Unit(t, s).Key.Check {
					found = true
					if row.Outcome != tc.outcome || ((tc.outcome == "skipped" || tc.outcome == "not_run") && row.Exercised) {
						t.Fatalf("wrong test outcome: %+v", row)
					}
				}
			}
			if !found || r.Success != (tc.outcome == "passed") {
				t.Fatalf("required test falsely credited: %+v", r)
			}
			old, _ := json.Marshal(r)
			pa318Success(t, context.Background(), s)
			after, _ := json.Marshal(r)
			if !bytes.Equal(old, after) || selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("driver changed runtime evidence or original files")
			}
		})
	}
}

func TestPythonApplicationExecutionKeepsRuntimeOutcomesIndependent(t *testing.T) {
	pa318RuntimeIndependence(t, false)
}

func pa318Freshness(t *testing.T, real bool) {
	t.Helper()
	for _, mode := range []string{"same invocation replay", "other invocation replay", "during execution", "between executions", "runner changed"} {
		t.Run(mode, func(t *testing.T) {
			root, p := pa318Fixture(t, false, pa318Functional, false)
			if mode == "other invocation replay" {
				v := p.Manifest.Components.Checks[0].Invocations[1]
				v.ID = "another"
				p.Manifest.Components.Checks[0].Invocations = append(p.Manifest.Components.Checks[0].Invocations, v)
				pa310Save(t, p)
			}
			before := pa315Tree(t, root)
			n := 0
			opts := pa318Options(t, real, &n)
			execute := opts.Execute
			if execute == nil {
				execute = RunPythonApplicationCommand
			}
			var previous []byte
			var command PythonApplicationCommand
			calls := 0
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				calls++
				command = c
				process, err := execute(ctx, c)
				if calls == 1 {
					previous = bytes.Clone(process.Events)
					return process, err
				}
				switch mode {
				case "same invocation replay", "other invocation replay":
					process.Events = bytes.Clone(previous)
				case "during execution":
					pa310Write(t, c.Root, "siblings/unselected.py", "VALUE=999\n")
				}
				return process, err
			}
			s := pa315Prepare(t, p, opts)
			pa318Success(t, context.Background(), s)
			if len(previous) == 0 {
				t.Fatal("real runner produced no transport")
			}
			if mode == "between executions" {
				pa310Write(t, command.Root, "tasks/next.txt", "done\n")
			}
			if mode == "runner changed" {
				pa310Write(t, command.RuntimeDir, "runner.py", "raise RuntimeError('changed runner')\n")
			}
			key := pa318Driver(t, s).Key
			if mode == "other invocation replay" {
				for _, v := range s.Invocations() {
					if v.Key.ID == "another" {
						key = v.Key
					}
				}
			}
			r, err := s.Run(context.Background(), key)
			pa318Reject(t, r, err)
			if (mode == "between executions" || mode == "runner changed") && calls != 1 {
				t.Fatal("stale input executed")
			}
			if n != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("freshness failure changed original or resolved dependencies again")
			}
		})
	}
}

func TestPythonApplicationExecutionObservationFreshness(t *testing.T) {
	pa318Freshness(t, false)
}

func TestPythonApplicationExecutionObservationsReal(t *testing.T) {
	// Meaningful actual-Python behavior always runs, so no opt-out SKIP can
	// disguise this acceptance test. Explicit opt-in MUST run real Docker.
	pa318Positive(t, false, false, false)
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if b, err := toolchain.Docker(ctx, "info").CombinedOutput(); err != nil {
		t.Fatalf("real suite requested but Docker unavailable: %v\n%s", err, b)
	}
	for _, wheel := range []bool{false, true} {
		for _, method := range []bool{false, true} {
			t.Run(fmt.Sprintf("wheel=%v/method=%v", wheel, method), func(t *testing.T) { pa318Positive(t, true, wheel, method) })
		}
	}
	t.Run("negative drivers", func(t *testing.T) { pa318Negative(t, true) })
	t.Run("freshness", func(t *testing.T) { pa318Freshness(t, true) })
	t.Run("runtime outcomes", func(t *testing.T) { pa318RuntimeIndependence(t, true) })
}
