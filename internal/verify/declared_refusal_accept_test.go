package verify

// Issue #392 acceptance contract (D-0200 refinement, merger records decision).
//
// Public Python API: Refusal(exception_type, **attributes), passed as the
// keyword-only Step(..., refusal=...) option. None preserves the old rule.
// exception_type is an Exception subclass (not an instance, tuple, or
// BaseException control-flow class). Match type(exc) by identity, never
// isinstance. Attributes are exact finite JSON scalars: None, bool, int,
// float, str, with type-sensitive equality, no coercion or subclass values.
// Missing attributes fail. Capture only declared attributes, not arbitrary
// exception messages/objects. A declaration with no attributes matches only
// the exact exception type. Reject malformed declarations before exploration.
//
// Wire contract: a declared exploration step adds
// "refusal":{"type":"module.QualifiedName","attributes":{...}}.
// Each transition of that step adds outcome:"accepted" or "refused";
// only a refused transition adds "refusal" with that same descriptor.
// The descriptor uses the actual exact exception class's module and qualname.
// Go independently validates declaration/outcome/exception consistency,
// scalar values, called:false and identical states for refusals. Reject
// missing/null/unknown/contradictory fields, and metadata on undeclared,
// sampled, initial or omitted rows. Preserve authentication and fail closed:
// a bad stream contributes no observations or exploration evidence. Replays
// must reproduce outcomes as well as state. An exploration must still call
// the selected target at least once. Failed calls cannot be laundered by
// catching a collector error in the driver.
//
// Converted attempts use [from,name,args,to,{"outcome":"accepted"}] or
// [from,name,args,to,{"outcome":"refused","refusal":descriptor}].
// Undeclared steps keep four fields. Conversion/merging must not drop the
// fifth field. Exploration.refusals counts actual declared refusals plus
// unchanged legacy attempts, NOT declared normal read-only returns;
// exploration.declared_refusals counts just declared refusals and is omitted
// for explorations without declarations. Counts exclude replays. Strict
// fields participate in portable fingerprints and survive JSON round-trips.
//
// The collector must distinguish exception propagation from coroutine yield,
// resumption and stackless await-completion events in Python 3.12 and 3.13.
// Exception handling must not weaken existing identity, read-back, thread,
// task completion, timeout or selected-call rules. A refusal may propagate
// through ordinary helpers on the entry's own stack; a target ever entered,
// any started application thread (even joined), or failed detached async task
// disqualifies it. Only the escaped declared exception can be a refusal.
// Existing functional and instance forms support synchronous declarations
// under their existing restrictions; module form additionally supports async.
//
// Real subtests run when either existing Python execution/gate real suite is
// requested. They are intentionally nested in tests with meaningful local
// assertions, not top-level skipped tests. Missing opted-in tools fail.

import (
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
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
)

const refusal392Source = `import asyncio, threading
counts = {'value': 0}
lock = threading.Lock()
class Refused(Exception):
    def __init__(self, status_code):
        self.status_code = status_code
        self.unrecorded = object()
class Child(Refused):
    pass
def increment(value):
    counts['value'] = value
async def submit(value):
    await asyncio.sleep(0)
    with lock:
        if counts['value'] != 0 or value != 1:
            raise Refused(409)
        increment(value)
    await asyncio.sleep(0)
def read(value):
    with lock:
        if counts['value'] != value:
            raise Refused(404)
        return counts['value']
def legacy():
    return None
`

const refusal392Driver = `from invariant_observe import Refusal, Step, explore, module
from counter import core as app

def start():
    app.counts['value'] = 0
    return module(app.increment, fields={'count':'counts.value'})
def submit(life, state, value):
    life.step(app.submit, value)
def read(life, state, value):
    life.step(app.read, value)
def legacy(life, state):
    life.step(app.legacy)
def drive():
    explore(start, [Step('Submit', submit, [[1],[2]], refusal=Refusal(app.Refused, status_code=409)),
                    Step('Read', read, [[1],[2]], refusal=Refusal(app.Refused, status_code=404)),
                    Step('Legacy', legacy)])
if __name__ == '__main__':
    drive()
`

const refusal392Model = `---- MODULE Counter ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLE count
vars == <<count>>
Init == count = 0
Submit(k) == count = 0 /\ k = 1 /\ count' = k
Read(k) == count = k /\ UNCHANGED count
Legacy == UNCHANGED count
Next == (\E k \in 1..2 : Submit(k) \/ Read(k)) \/ Legacy
Spec == Init /\ [][Next]_vars
Safe == count \in 0..1 /\ Helper
Full == count = 1
Overflow == count' = 2
====
`

func refusal392Fixture(t *testing.T, explicit bool, source, driver, model string) pa342Fixture {
	t.Helper()
	f := pa342New(t, explicit, false)
	pa342Write(t, f.root, pa342Source, source)
	pa342Write(t, f.root, pa342DriverPath, driver)
	pa342Write(t, f.root, pa342ModelPath, model)
	pa342Write(t, f.root, "tests/test_counter.py", "import unittest\nfrom counter import core\nclass CounterTests(unittest.TestCase):\n    def test_inc(self):\n        core.increment(1)\n        self.assertEqual(core.counts['value'], 1)\n")
	for i := range f.p.Lock.Statements {
		s := &f.p.Lock.Statements[i]
		h, err := tla.PinHash(model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		s.SHA256 = h
	}
	pa342Save(t, f)
	return f
}

func refusal392Modes(t *testing.T, run func(*testing.T, bool)) {
	t.Helper()
	t.Run("actual local Python", func(t *testing.T) { run(t, false) })
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") == "1" || os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real pinned Docker Python", func(t *testing.T) { run(t, true) })
	}
}

// Fail promptly when the public API is absent, before repeatedly discovering
// the fixture's sources. Once available, all actual execution/gate assertions
// below still run; an exported name alone never makes an acceptance test pass.
func refusal392API(t *testing.T) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "invariant_observe.py"), pythonApplicationObserver, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-S", "-B", "-c", "import sys; sys.path.insert(0, sys.argv[1]); from invariant_observe import Refusal, Step; assert callable(Refusal) and callable(Step)", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("declared-refusal public API: %v\n%s", err, out)
	}
}

func refusal392Execution(t *testing.T, real bool, source, driver string, sampled bool) *PythonApplicationExecutionResult {
	t.Helper()
	f := refusal392Fixture(t, true, source, driver, refusal392Model)
	f.p.Manifest.Exhaustive = !sampled
	pa342Save(t, f)
	before := pa342Tree(t, f.root)
	s := pa315Prepare(t, f.p, pa318Options(t, real, new(int)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	x, err := s.Run(ctx, pa318Driver(t, s).Key)
	if x == nil || (err != nil && x.Success) {
		t.Fatalf("execution: %+v %v", x, err)
	}
	if !reflect.DeepEqual(before, pa342Tree(t, f.root)) {
		t.Fatal("execution changed originals")
	}
	return x
}

func refusal392Wire(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func refusal392Assert(t *testing.T, x *PythonApplicationExecutionResult) {
	t.Helper()
	if !x.Success || !x.Complete || x.Exploration == nil {
		t.Fatalf("valid declared refusals must complete: %+v", x)
	}
	w := refusal392Wire(t, x.Exploration)
	if x.Exploration.States != 2 || x.Exploration.Attempts != 10 || x.Exploration.Refusals != 8 || w["declared_refusals"] != json.Number("6") {
		t.Fatalf("read-only acceptance was counted as refusal or counts lost: %v", w)
	}
	refused, accepted, legacy := 0, 0, 0
	for _, row := range x.Observations {
		if row.Kind != "transition" || row.Replay == nil || *row.Replay {
			continue
		}
		wire := refusal392Wire(t, row)
		if row.Step == "Legacy" {
			if wire["outcome"] != nil || wire["refusal"] != nil {
				t.Fatal("legacy row gained strict metadata")
			}
			legacy++
			continue
		}
		wantAccepted := row.Step == "Submit" && row.Before["count"] == json.Number("0") && row.Args[0] == json.Number("1") || row.Step == "Read" && row.Before["count"] == json.Number("1") && row.Args[0] == json.Number("1")
		if wantAccepted {
			accepted++
			if wire["outcome"] != "accepted" || wire["refusal"] != nil {
				t.Fatalf("normal return is acceptance: %v", wire)
			}
			if row.Step == "Read" && (row.Called == nil || *row.Called || !reflect.DeepEqual(row.Before, row.After)) {
				t.Fatalf("read-only return: %+v", row)
			}
		} else {
			refused++
			code := json.Number("409")
			if row.Step == "Read" {
				code = "404"
			}
			want := map[string]any{"type": "counter.core.Refused", "attributes": map[string]any{"status_code": code}}
			if wire["outcome"] != "refused" || !reflect.DeepEqual(wire["refusal"], want) || row.Called == nil || *row.Called || !reflect.DeepEqual(row.Before, row.After) {
				t.Fatalf("wrong refusal evidence: %v", wire)
			}
		}
	}
	if refused != 6 || accepted != 2 || legacy != 2 {
		t.Fatalf("outcomes %d/%d/%d", refused, accepted, legacy)
	}
	for _, step := range x.Exploration.Steps {
		w := refusal392Wire(t, step)
		if step.Name != "Legacy" && w["refusal"] == nil {
			t.Fatalf("declaration lost: %v", w)
		}
	}
	b, _ := json.Marshal(x)
	var back PythonApplicationExecutionResult
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refusal392Wire(t, x), refusal392Wire(t, back)) {
		t.Fatal("JSON round trip lost strict evidence")
	}
	raw, err := pythonApplicationAttempts(*x)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergePythonApplicationAttempts([][]byte{raw, raw})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		raw   []byte
		times int
	}{{raw, 1}, {merged, 2}} {
		var rec struct {
			Attempts [][]json.RawMessage `json:"attempts"`
		}
		if err := json.Unmarshal(item.raw, &rec); err != nil {
			t.Fatal(err)
		}
		strict, old := 0, 0
		for _, a := range rec.Attempts {
			var name string
			_ = json.Unmarshal(a[1], &name)
			if name == "Legacy" {
				old++
				if len(a) != 4 {
					t.Fatal("legacy attempt changed")
				}
				continue
			}
			strict++
			if len(a) != 5 {
				t.Fatalf("outcome lost in attempts: %s", item.raw)
			}
			var m map[string]any
			_ = json.Unmarshal(a[4], &m)
			if m["outcome"] != "accepted" && m["outcome"] != "refused" {
				t.Fatalf("missing outcome: %s", a[4])
			}
			if m["outcome"] == "refused" && m["refusal"] == nil {
				t.Fatal("exception lost in attempt")
			}
		}
		if strict != 8*item.times || old != 2*item.times {
			t.Fatal("attempts dropped in merge")
		}
	}
}

func TestPythonApplicationExecutionDeclaredRefusals(t *testing.T) {
	refusal392API(t)
	refusal392Modes(t, func(t *testing.T, real bool) {
		refusal392Assert(t, refusal392Execution(t, real, refusal392Source, refusal392Driver, false))
		syncSource := strings.ReplaceAll(strings.Replace(refusal392Source, "async def submit", "def submit", 1), "    await asyncio.sleep(0)\n", "")
		t.Run("synchronous entry", func(t *testing.T) {
			refusal392Assert(t, refusal392Execution(t, real, syncSource, refusal392Driver, false))
		})
		helper := strings.Replace(refusal392Source, "raise Refused(409)", "deny()", 1) + "\ndef deny():\n    raise Refused(409)\n"
		t.Run("helper on own stack", func(t *testing.T) { refusal392Assert(t, refusal392Execution(t, real, helper, refusal392Driver, false)) })
		returned := strings.Replace(refusal392Source, "async def submit(value):", "def submit(value):\n    return submit_async(value)\nasync def submit_async(value):", 1)
		t.Run("returned awaitable", func(t *testing.T) {
			refusal392Assert(t, refusal392Execution(t, real, returned, refusal392Driver, false))
		})
		noAttrs := strings.ReplaceAll(strings.ReplaceAll(refusal392Driver, ", status_code=409", ""), ", status_code=404", "")
		x := refusal392Execution(t, real, refusal392Source, noAttrs, false)
		if !x.Success {
			t.Fatalf("type-only declaration failed: %q", x.Diagnostics)
		}
		for _, row := range x.Observations {
			w := refusal392Wire(t, row)
			if w["outcome"] == "refused" {
				d := w["refusal"].(map[string]any)
				if !reflect.DeepEqual(d["attributes"], map[string]any{}) {
					t.Fatal("undeclared attributes leaked")
				}
			}
		}
		// An all-legacy driver keeps the same JSON shape and normal-return rule.
		oldDriver := strings.ReplaceAll(strings.ReplaceAll(refusal392Driver, ", refusal=Refusal(app.Refused, status_code=409)", ""), ", refusal=Refusal(app.Refused, status_code=404)", "")
		oldSource := strings.ReplaceAll(strings.ReplaceAll(refusal392Source, "raise Refused(409)", "return None"), "raise Refused(404)", "return None")
		old := refusal392Execution(t, real, oldSource, oldDriver, false)
		if !old.Success || old.Exploration.Refusals != 9 || refusal392Wire(t, old.Exploration)["declared_refusals"] != nil {
			t.Fatalf("legacy behavior changed: %+v", old)
		}
	})
}

func TestPythonApplicationExecutionDeclaredRefusalFailures(t *testing.T) {
	refusal392API(t)
	refusal392Modes(t, func(t *testing.T, real bool) {
		// A blanket rejection of the feature cannot satisfy these negatives.
		refusal392Assert(t, refusal392Execution(t, real, refusal392Source, refusal392Driver, false))
		cases := []struct {
			name, source, driver string
			sampled              bool
		}{
			{name: "undeclared exception", source: strings.Replace(refusal392Source, "raise Refused(409)", "raise RuntimeError('unexpected')", 1)},
			{name: "subclass", source: strings.Replace(refusal392Source, "raise Refused(409)", "raise Child(409)", 1)},
			{name: "wrong attribute", source: strings.Replace(refusal392Source, "raise Refused(409)", "raise Refused(403)", 1)},
			{name: "wrong scalar type", source: strings.Replace(refusal392Source, "raise Refused(409)", "raise Refused(409.0)", 1)},
			{name: "missing attribute", source: strings.Replace(refusal392Source, "raise Refused(409)", "error = Refused(409)\n            del error.status_code\n            raise error", 1)},
			{name: "changed state", source: strings.Replace(refusal392Source, "raise Refused(409)", "counts['value'] = 9\n            raise Refused(409)", 1)},
			{name: "target raised", source: strings.Replace(refusal392Source, "counts['value'] = value", "raise Refused(409)", 1)},
			{name: "after target returned and state restored", source: strings.Replace(refusal392Source, "increment(value)\n", "increment(value)\n        counts['value'] = 0\n        raise Refused(409)\n", 1)},
			{name: "target caught exception", source: strings.Replace(refusal392Source, "counts['value'] = value", "try:\n        raise Refused(409)\n    except Refused:\n        counts['value'] = value", 1)},
			{name: "worker raised", source: strings.Replace(refusal392Source, "raise Refused(409)", "worker = threading.Thread(target=deny)\n            worker.start()\n            worker.join()\n            return None", 1) + "\ndef deny():\n    raise Refused(409)\n"},
			{name: "joined worker before refusal", source: strings.Replace(refusal392Source, "raise Refused(409)", "worker = threading.Thread(target=noop)\n            worker.start()\n            worker.join()\n            raise Refused(409)", 1) + "\ndef noop():\n    pass\n"},
			{name: "exception instance declaration", driver: strings.Replace(refusal392Driver, "Refusal(app.Refused, status_code=409)", "Refusal(app.Refused(409))", 1)},
			{name: "control flow declaration", driver: strings.Replace(refusal392Driver, "Refusal(app.Refused, status_code=409)", "Refusal(KeyboardInterrupt)", 1)},
			{name: "container attribute", driver: strings.Replace(refusal392Driver, "status_code=409", "status_code=[409]", 1)},
			{name: "nonfinite attribute", driver: strings.Replace(refusal392Driver, "status_code=409", "status_code=float('nan')", 1)},
			{name: "sampled explore declaration", sampled: true},
			{name: "sampled application exception", sampled: true, driver: `from invariant_observe import Refusal, module
from counter import core as app
DECLARED = Refusal(app.Refused, status_code=409)
def drive():
    with module(app.increment, fields={'count':'counts.value'}) as life:
        life.step(app.submit, 1)
        life.step(app.submit, 1)
if __name__ == '__main__': drive()
`},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if c.source == "" {
					c.source = refusal392Source
				}
				if c.driver == "" {
					c.driver = refusal392Driver
				}
				x := refusal392Execution(t, real, c.source, c.driver, c.sampled)
				if x.Success || x.Complete || x.Exploration != nil || len(x.Observations) != 0 || len(x.Diagnostics) == 0 {
					t.Fatalf("invalid outcome earned evidence: %+v", x)
				}
			})
		}
	})
}

// The default gate transport executes real Python but records TLC requests;
// real TLC semantics are tested below when the real gate suite is requested.
// Reuse transport plumbing only, replacing the old counter-model simulator.
func refusal392GateFake(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	tc, log := pa342Fake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(filepath.Dir(docker), "docker.py")
	b, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	from := strings.Index(body, "    if module=='Counter':")
	if from < 0 {
		t.Fatal("transport fixture changed")
	}
	to := strings.Index(body[from:], "    passed(3)")
	if to < 0 {
		t.Fatal("transport fixture changed")
	}
	body = body[:from] + `    record({'kind':'obligation','module':module,'body':body,'cfg':cfg})
    if module.startswith('Witness_'):
        msg(2110,'Invariant Invariant_Never_Full is violated.'); msg(2217,'1: <Initial predicate>\n/\\ count = 1'); sys.exit(12)
    if module.startswith('Bug_'):
        msg(2110,'Invariant Safe is violated.'); msg(2217,'1: <Initial predicate>\n/\\ count = 2'); sys.exit(12)
    passed(2,2)
` + body[from+to+len("    passed(3)\n"):]
	if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return tc, log
}

func TestPythonApplicationGateDeclaredRefusals(t *testing.T) {
	refusal392API(t)
	run := func(t *testing.T, tc toolchain.Toolchain, log string, real bool) {
		for _, explicit := range []bool{true, false} {
			t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
				f := refusal392Fixture(t, explicit, refusal392Source, refusal392Driver, refusal392Model)
				r := pa342Unchanged(t, f, tc)
				if !r.Passed {
					b, _ := json.Marshal(r)
					t.Fatalf("valid refusal gate: %s", b)
				}
				x, c := pa342Explored(t, r)
				refusal392Assert(t, x)
				if !c.passed() || c.Result.States != 2 {
					t.Fatalf("missing explored check: %+v", c)
				}
				if explicit {
					row := pa342Row(t, r)
					if !row.EveryStep || !row.ExhaustiveStates {
						t.Fatalf("qualified evidence lost: %+v", row)
					}
				}
				if r.EverySize() || r.Claim() == "proved at every size" {
					t.Fatal("exploration claimed proof")
				}
				if !real {
					b, err := os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					text := string(b)
					if !strings.Contains(text, "ENABLED") || !strings.Contains(text, "Submit(") || !strings.Contains(text, "Read(") {
						t.Fatal("TLC never received named outcome obligations")
					}
				}
			})
		}
		if !real {
			return
		}
		for _, c := range []struct{ name, model, source string }{
			{"refused while model enables", strings.Replace(refusal392Model, "Read(k) == count = k", "Read(k) == k = 1", 1), refusal392Source},
			{"normal return while model disables", strings.Replace(refusal392Model, "Read(k) == count = k", "Read(k) == FALSE /\\ count = k", 1), refusal392Source},
			{"wrong named action despite valid Next", strings.Replace(refusal392Model, "Submit(k) == count = 0 /\\ k = 1 /\\ count' = k", "Submit(k) == FALSE /\\ count' = k", 1), refusal392Source},
		} {
			t.Run(c.name, func(t *testing.T) {
				// Keep all states model-reachable in the wrong-action case;
				// Next permits the transition, just not the named Submit.
				if c.name == "wrong named action despite valid Next" {
					c.model = strings.Replace(c.model, "Legacy == UNCHANGED count", "Legacy == count' \\in 0..1", 1)
				}
				f := refusal392Fixture(t, true, c.source, refusal392Driver, c.model)
				r := pa342Unchanged(t, f, tc)
				x, check := pa342Explored(t, r)
				if !x.Success {
					t.Fatalf("model mismatch must retain successful execution: %q", x.Diagnostics)
				}
				if r.Passed || check.passed() {
					t.Fatalf("TLC accepted false declared outcome: %+v", check)
				}
				row := pa342Row(t, r)
				if row.ExhaustiveStates || row.EveryStep {
					t.Fatalf("failed outcome gained coverage: %+v", row)
				}
			})
		}
	}
	t.Run("actual Python and TLC transport", func(t *testing.T) { tc, log := refusal392GateFake(t); run(t, tc, log, false) })
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python and TLC", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			run(t, tc, "", true)
		})
	}
}
