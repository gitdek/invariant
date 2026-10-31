package verify_test

// Module raw JSON observations flow through D-0185's existing gate, for both
// explicit and omitted components. At that boundary only, recursively translate
// module lists to conformance $seq values and ordinary dicts to records. Do not
// interpret application dict keys as conformance tags ($mv/$seq/$fn/$set): if
// their keys cannot encode as ordinary record fields, fail with a diagnostic.
// Preserve exact integers and raw receipt state; nil/fractions and other values
// the existing TLC encoder cannot represent still fail explicitly. Do not
// broaden the shared legacy conformance encoding or claim proof/exhaustiveness.
// Local mode executes real Python and records TLC requests through a transport
// double. It is not real TLC validation. Opt-in uses actual Docker/Python/TLC:
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify \
//   -run '^TestPythonApplicationGateModuleObservations$' -count=1 -timeout 30m
// Also run INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1 go test ./internal/verify
// -run '^TestPythonApplicationExecutionModule' -count=1 -timeout 30m on final head.
// Missing prerequisites fail opted-in checks. Unavailable Docker/network and
// recording the new decision belong under Merger work, not mandatory repair.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
)

const pa321GateModel = `---- MODULE Lifecycle ----
EXTENDS Naturals, Sequences, Local
CONSTANT Limit
VARIABLE status
vars == <<status>>
Init == status = <<>>
Next == IF Len(status) < Limit THEN status' = Append(status, "done") ELSE UNCHANGED status
Spec == Init /\ [][Next]_vars
Safe == status \in UNION { [1..n -> {"done"}] : n \in 0..Limit } /\ Helper
Seen == Len(status) = Limit
Bad == status' = Append(status, "broken")
Fair == WF_vars(Next)
Eventually == <> (Len(status) = Limit)
====
`

const pa321GateSource = `import asyncio, threading, time, uuid
tasks = {}
lock = threading.Lock()
def register(fn): return fn
def finish(key, status):
    time.sleep(0.02)
    tasks[key]['status'] = status
@register
async def advance(status):
    await asyncio.sleep(0)
    with lock:
        key = uuid.uuid4().hex
        tasks[key] = {'status': 'reserved'}
    threading.Thread(target=finish, args=(key, status)).start()
    await asyncio.sleep(0)
def submit(status):
    return advance(status)
`

const pa321GateDriver = `from invariant_observe import module
from tasks import lifecycle as app
def drive():
    with module(app.advance, fields={'status':'tasks[*].status'}, expected_initial={'status':[]}) as life:
        life.step(app.submit, 'done', expected_before={'status':[]}, expected_after={'status':['done']})
        life.step(app.submit, 'done', expected_after={'status':['done','done']})
if __name__ == '__main__': drive()
`

func pa321GateFixture(t *testing.T, explicit bool) pa327Fixture {
	t.Helper()
	f := pa327New(t, explicit, false)
	pa327Write(t, f.root, pa327Source, pa321GateSource)
	pa327Write(t, f.root, pa327DriverPath, pa321GateDriver)
	pa327Write(t, f.root, pa327ModelPath, pa321GateModel)
	pa327Write(t, f.root, "tests/test_tasks.py", "import unittest\nfrom tasks import lifecycle\nclass TaskTests(unittest.TestCase):\n    def test_pass(self):\n        self.assertEqual(lifecycle.tasks, {})\n")
	// These are new test-fixture statements, not repository pins or decisions.
	for i := range f.p.Lock.Statements {
		s := &f.p.Lock.Statements[i]
		h, err := tla.PinHash(pa321GateModel, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		s.SHA256 = h
	}
	pa327Save(t, f)
	return f
}

func pa321GateExercise(t *testing.T, tc toolchain.Toolchain, log string) {
	t.Helper()
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("components=%v", explicit), func(t *testing.T) {
			f := pa321GateFixture(t, explicit)
			before := pa327Tree(t, f.root)
			r := pa327Run(t, f, tc)
			pa327Assert(t, f, r, true)
			app := pa327Application(t, r)
			found := false
			for _, execution := range app.Executions {
				if execution.Key.Kind != "conformance" {
					continue
				}
				found = true
				rows := execution.Observations
				if len(rows) != 3 {
					t.Fatalf("module observations lost: %+v", execution)
				}
				for i, row := range rows {
					state := row.State
					if i > 0 {
						state = row.After
					}
					want := []string{}
					for j := 0; j < i; j++ {
						want = append(want, "done")
					}
					if string(pa327JSON(t, state["status"])) != string(pa327JSON(t, want)) {
						t.Fatalf("receipt lost ordered raw lists: %+v", row)
					}
				}
			}
			if !found {
				t.Fatal("no module execution")
			}
			if len(app.Conformance) != 1 {
				t.Fatal("no sampled TLC result")
			}
			md := receipt.Markdown(r)
			if !strings.Contains(md, "sampled") || !strings.Contains(md, "tasks[*].status") {
				t.Fatalf("receipt omits module read-back: %s", md)
			}
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("gate changed originals")
			}
			second := pa327Run(t, f, tc)
			if !second.Passed || r.Fingerprint != second.Fingerprint {
				t.Fatal("generated task keys changed portable gate fingerprint")
			}
			if log != "" {
				b, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				init, next := false, false
				for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					var event struct{ Kind, Module, Body string }
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					if event.Kind != "tlc" {
						continue
					}
					if event.Module == "Invariant_ConformanceInit" {
						init = strings.Contains(event.Body, "<<>>")
					}
					if event.Module == "Invariant_Conformance" {
						next = strings.Contains(event.Body, `<<"done", "done">>`)
					}
				}
				if !init || !next {
					t.Fatal("module list was not checked as an ordered TLC sequence")
				}
			}
			// A genuinely bad model transition still has valid collector evidence;
			// expected values agree with the application, so TLC must reject it.
			pa327Write(t, f.root, pa327DriverPath, strings.ReplaceAll(pa321GateDriver, "done", "broken"))
			bad := pa327Run(t, f, tc)
			pa327Assert(t, f, bad, false)
			badApp := pa327Application(t, bad)
			unit, driver := false, false
			for _, execution := range badApp.Executions {
				if execution.Key.Kind == "test" && execution.Success {
					unit = true
				}
				if execution.Key.Kind == "conformance" && execution.Success && len(execution.Observations) == 3 {
					driver = true
				}
			}
			if !unit || !driver || len(badApp.Conformance) != 1 {
				t.Fatal("TLC failure erased independent execution evidence")
			}
			if bad.Fingerprint == r.Fingerprint {
				t.Fatal("changed observations did not change receipt fingerprint")
			}
		})
	}
}

func TestPythonApplicationGateModuleObservations(t *testing.T) {
	t.Run("actual Python and recorded TLC requests", func(t *testing.T) {
		tc, log := pa327Fake(t)
		pa321GateExercise(t, tc, log)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker and TLC", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			tc, err := toolchain.Ensure(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pa321GateExercise(t, tc, "")
		})
	}
}
