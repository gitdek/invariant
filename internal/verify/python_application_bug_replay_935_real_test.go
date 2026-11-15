package verify

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Exercise the scalar and held-call acceptance fixtures through the pinned
// image as well as the local collector tests. These records make no TLC claim.
func TestPythonApplicationBugReplayRealLifecycles935(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" && os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") != "1" {
		t.Skip("set INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1 for pinned Python")
	}
	t.Run("scalar", pa935ScalarFormsReal)
	t.Run("held", func(t *testing.T) { pa935HeldCalls(t, true, false) })
	t.Run("held cleanup failure", func(t *testing.T) { pa935HeldCalls(t, true, true) })
}

func TestPythonApplicationBugReplayHeldCleanup935(t *testing.T) {
	t.Parallel()
	pa935HeldCalls(t, false, true)
}

func pa935ScalarFormsReal(t *testing.T) {
	t.Helper()
	const model = `---- MODULE Lifecycle ----
EXTENDS Naturals
CONSTANT Token
VARIABLE task
Init == task = 0
Advance == task = 0 /\ task' = 1
Next == Advance \/ UNCHANGED task
Spec == Init /\ [][Next]_task
Safe == task <= 1
Again == task = 1 /\ task' = 2
ReplayCompletedTask == Again
ReuseRecoveredName == Again
====
`
	for _, method := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("scalar/method=%v/explicit=%v", method, explicit), func(t *testing.T) {
				source := `class Refused(Exception):
    pass
def start():
    return 0
def advance(state):
    return state + 1
def tick(state):
    if state >= 1:
        raise Refused()
    return advance(state)
`
				driver := `from invariant_observe import functional, Step, Refusal, replay
from tasks import lifecycle as app
def start():
    return functional(app.advance, app.start, argument='state', field='task')
def take(life, state):
    life.step(app.tick)
def drive():
    if replay(start, [Step('Advance',take), Step('Again',take,refusal=Refusal(app.Refused))]):
        return
    with start() as life:
        life.step(app.tick)
if __name__ == '__main__': drive()
`
				root, p := pa935Fixture(t, explicit, "fixed", driver)
				if method {
					source = "class Refused(Exception):\n    pass\nclass Task:\n    def __init__(self):\n        self.state = 0\n    def advance(self):\n        self.state += 1\n    def tick(self):\n        if self.state >= 1:\n            raise Refused()\n        self.advance()\n"
					driver = strings.ReplaceAll(driver, "functional, Step", "instance, Step")
					driver = strings.Replace(driver, "def start():\n    return functional(app.advance, app.start, argument='state', field='task')", "objects = []\ndef start():\n    obj = app.Task()\n    objects[:] = [obj]\n    return instance(obj.advance, fields={'task':'state'})", 1)
					driver = strings.ReplaceAll(driver, "app.tick", "objects[0].tick")
					if explicit {
						p.Manifest.Components.Checks[0].Target.Symbol = "Task.advance"
					}
				}
				pa310Write(t, root, pa315Target, source)
				pa310Write(t, root, pa315Project+"/driver.py", driver)
				path := pa935Request(t, p, false).Path
				state := func(n string) tlc.ReplayState { return tlc.ReplayState{"task": {Kind: "integer", Atom: n}} }
				path.States, path.Steps, path.Offending = []tlc.ReplayState{state("0"), state("1"), state("2")}, []tlc.ReplayStep{{From: 0, To: 1, Action: "Advance"}, {From: 1, To: 2, Action: "Again", Bug: true}}, 1
				q := pa935Rebind(t, p, path, model)
				s := pa315Prepare(t, p, pa318Options(t, true, new(int)))
				x := pa935Run(t, s, q)
				w := pa935RawOf(t, x)
				if w.Result.Outcome != "refused" || len(x.Observations) != 3 || x.Observations[2].Refusal == nil {
					t.Fatalf("scalar form did not record actual guard refusal: %+v", x)
				}
			})
		}
	}
}

func pa935HeldCalls(t *testing.T, real, brokenCleanup bool) {
	pa935API(t, &PythonApplicationSession{})
	driver := strings.Replace(hold393Driver, "Refusal, Step, explore, module", "Refusal, Step, explore, module, replay", 1)
	driver = strings.Replace(driver, "    explore(start, [Step", "    if replay(start, [Step('Submit', submit, [[1]], refusal=Refusal(app.Refused, status_code=409)), Step('Finish', finish), Step('Busy', submit, [[1]], refusal=Refusal(app.Refused, status_code=409))]):\n        return\n    explore(start, [Step", 1)
	source := hold393Source
	if brokenCleanup {
		source = "released = [0]\n" + strings.Replace(source, "def increment(value):", "def increment(value):\n    released[0] += 1\n    if released[0] == 2: raise RuntimeError('held cleanup failed')", 1)
	}
	f := hold393Fixture(t, true, source, driver)
	for i := range f.p.Lock.Statements {
		if f.p.Lock.Statements[i].Kind == project.Bug {
			f.p.Lock.Statements[i].CodeBehavior = "current"
		}
	}
	state := func(count, active int, prepared bool) tlc.ReplayState {
		return tlc.ReplayState{"count": {Kind: "integer", Atom: fmt.Sprint(count)}, "active": {Kind: "integer", Atom: fmt.Sprint(active)}, "prepared": {Kind: "bool", Atom: map[bool]string{true: "TRUE", false: "FALSE"}[prepared]}}
	}
	path := tlc.ReplayPath{Version: 1, Module: "Counter", Bug: "Overflow", Expect: "Safe", ExpectKind: "invariant", Specification: "Spec", Offending: 3, Tool: tlc.ReplayTool{Image: toolchain.JavaImage, JarSHA256: pythonApplicationHash([]byte("fixture jar"))},
		States: []tlc.ReplayState{state(0, 0, false), state(1, 1, true), state(2, 0, true), state(1, 1, true), state(1, 2, true)},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Submit", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}}, {From: 1, To: 2, Action: "Finish"}, {From: 2, To: 3, Action: "Submit", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}}, {From: 3, To: 4, Action: "Busy", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}, Bug: true}}}
	body := strings.Replace(hold393Model, "Overflow == count' = 1 /\\ active' = 2 /\\ prepared' = TRUE", "Busy(k) == k = 1 /\\ count' = 1 /\\ active' = 2 /\\ prepared' = TRUE\nOverflow == Busy(1)", 1)
	q := pa935Rebind(t, f.p, path, body)
	s := pa315Prepare(t, f.p, pa318Options(t, real, new(int)))
	if brokenCleanup {
		x, err := s.RunBugReplay(pa321Context(t), pa318Driver(t, s).Key, q)
		pa935Rejected(t, x, err)
		return
	}
	x := pa935Run(t, s, q)
	if len(x.Observations) != 5 {
		t.Fatalf("lost held operations: %+v", x)
	}
	for i, operation := range []string{"", "hold", "release", "hold", "hold"} {
		row := x.Observations[i]
		if row.Operation != operation {
			t.Fatalf("operation %d: %+v", i, row)
		}
		if i > 0 && (row.Called == nil || *row.Called != (i == 1 || i == 3)) {
			t.Fatalf("release/refusal invented a selected start: %+v", row)
		}
	}
	if pa935RawOf(t, x).Result.Outcome != "refused" || x.Observations[4].Refusal == nil {
		t.Fatal("held refusal not preserved")
	}
	// A fresh invocation must not inherit the previous held worker; completing
	// the first run includes resuming and draining that worker and descendants.
	pa935Run(t, s, q)
}
