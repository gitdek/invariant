package verify

// Issue #393 contract. No application source is rewritten to implement a hold.
//
// Public API on stateless handles:
//   life.hold(entry, *args, expected_before=None, expected_after=None, **kwargs)
//   life.release(*, expected_before=None, expected_after=None)
// Both are one explored take, subject to the same lifecycle, budget, binding,
// continuity and expectation checks as step(). Only module exploration admits
// them. Arguments to hold are entry inputs, including an application kwarg
// named hold. A hold pauses the first selected frame on an owned worker at its
// first event, before target code. An entry-stack target cannot be held.
// Completion requires the entry/awaitable and all other owned threads to end.
// A hold with no selected start fails, except a valid #392 declared refusal;
// a previously held worker is not new work by the refusing attempt. A call
// already completed without the gate's pause cannot be retrospectively held.
//
// release resumes that frame and drains its worker and descendants under one
// fresh module deadline. It invokes no new application entry. Empty release
// is the unchanged legacy D-0200 refusal; a release step has no Refusal
// declaration (reject one rather than fabricate an application exception).
// Any second selected start while held fails, even from ordinary step() or
// if the driver catches the collector error. Other steps and declared
// refusals keep their normal semantics while the worker is paused.
//
// Wire: only hold/release transitions add operation:"hold" or "release".
// called counts starts: successful hold=true, its release=false. An exception
// refusal during hold has called=false and leaves the prior hold intact.
// Release rows use target/source_sha256 as entry/entry_sha256, including an
// empty release; they do not claim another surrounding entry executed.
// Go validates operations, held status, form/mode, continuity and replay, and
// permits changed state with called=false only for a matching held release.
// Ordinary, initial and omitted rows omit operation. Metadata survives JSON
// and fingerprints; attempts retain model names and #392 outcome metadata.
//
// Replay reproduces operations as well as states/outcomes. Each run can end
// held: resume and drain before the next start, emit no cleanup observations,
// and fail the entire execution on unfinished cleanup. Owned events after
// release belong to that step; at any other time they poison execution.
// Preserve frame/object ownership, coroutine identity, independent watchdog,
// authentication and poisoning. No accepted prefix survives any failure.
// Document the new API and these exceptions to the old exactly-one-completed
// call rule in README/SPEC, including handle access rules and a Submit/Finish
// example. Cite existing decisions; the merger records the new refinement.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Derived from #392's async entry, exact exception and locked reservation.
// count: 0=idle, 1=queued, 3=running, 2=finished. The first target statement
// checks its reservation. A descendant waits for the target thread to exit,
// then frees the slot, so draining just the selected frame is insufficient.
const hold393Source = `import asyncio, threading
counts = {'value': 0, 'active': 0, 'prepared': False}
audit = {'pending': False, 'hang': False}
lock = threading.Lock()
class Refused(Exception):
    def __init__(self, status_code):
        self.status_code = status_code
        self.unrecorded = object()
def reset():
    assert not audit['pending'], 'previous run leaked held work or descendants'
    counts.update(value=0, active=0, prepared=False)
    audit['hang'] = False
def settle(parent):
    parent.join()
    counts['active'] = 0
    audit['pending'] = False
def increment(value):
    assert counts['value'] == 1
    counts['value'] = 3
    counts['value'] = 2
    threading.Thread(target=settle, args=(threading.current_thread(),)).start()
def prepare():
    counts['prepared'] = True
async def submit(value, *, hold='forwarded'):
    assert value == 1 and hold == 'forwarded'
    await asyncio.sleep(0)
    with lock:
        if counts['active'] != 0:
            raise Refused(409)
        counts['value'] = 1
        counts['active'] += 1
        counts['prepared'] = False
        audit['pending'] = True
    threading.Thread(target=increment, args=(value,)).start()
    threading.Thread(target=prepare).start()
    await asyncio.sleep(0)
def read():
    return counts['value']
`

const hold393Driver = `from invariant_observe import Refusal, Step, explore, module
from counter import core as app
def start():
    app.reset()
    return module(app.increment, fields={'count':'counts.value', 'active':'counts.active', 'prepared':'counts.prepared'}, timeout=2)
def submit(life, state, value):
    expected = state if state['active'] else {'count':1, 'active':1, 'prepared':True}
    life.hold(app.submit, value, hold='forwarded', expected_before=state, expected_after=expected)
def finish(life, state):
    expected = {'count':2, 'active':0, 'prepared':True} if state['active'] else state
    life.release(expected_before=state, expected_after=expected)
def read(life, state):
    life.step(app.read, expected_before=state, expected_after=state)
def drive():
    explore(start, [Step('Submit', submit, [[1]], refusal=Refusal(app.Refused, status_code=409)),
                    Step('Finish', finish), Step('Read', read, refusal=Refusal(app.Refused, status_code=404))])
if __name__ == '__main__': drive()
`

const hold393Model = `---- MODULE Counter ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLES count, active, prepared
vars == <<count, active, prepared>>
Init == count = 0 /\ active = 0 /\ prepared = FALSE
Submit(k) == active = 0 /\ k = 1 /\ count' = 1 /\ active' = active + 1 /\ prepared' = TRUE
Finish == active = 1 /\ count' = 2 /\ active' = 0 /\ UNCHANGED prepared
Read == UNCHANGED vars
Next == (\E k \in {1} : Submit(k)) \/ Finish \/ Read
Spec == Init /\ [][Next]_vars
Safe == active \in 0..1 /\ count \in 0..2 /\ (count = 1 <=> active = 1) /\ Helper
Full == count = 1 /\ active = 1
Overflow == count' = 1 /\ active' = 2 /\ prepared' = TRUE
====
`

func hold393Fixture(t *testing.T, explicit bool, source, driver string) pa342Fixture {
	t.Helper()
	f := refusal392Fixture(t, explicit, source, driver, hold393Model)
	// The independent configured test verifies the reservation's fresh state;
	// conformance, not this runtime test, exercises the selected threaded call.
	pa342Write(t, f.root, "tests/test_counter.py", "import unittest\nfrom counter import core\nclass CounterTests(unittest.TestCase):\n    def test_inc(self):\n        core.reset()\n        self.assertEqual(core.counts['active'], 0)\n")
	return f
}

// Fail promptly on missing public operation evidence, before preparing a
// Python execution. This is a JSON behavior check, not a source inspection;
// once it works, every actual-Python assertion below must still pass.
func hold393API(t *testing.T) {
	t.Helper()
	for _, operation := range []string{"hold", "release"} {
		var row PythonApplicationObservation
		if err := json.Unmarshal([]byte(`{"operation":"`+operation+`"}`), &row); err != nil {
			t.Fatal(err)
		}
		if wire := refusal392Wire(t, row); wire["operation"] != operation {
			t.Fatalf("hold/release operation evidence was discarded: %v", wire)
		}
	}
}

func hold393Execution(t *testing.T, real bool, source, driver string, sampled bool, elapsed ...*time.Duration) *PythonApplicationExecutionResult {
	t.Helper()
	f := hold393Fixture(t, true, source, driver)
	f.p.Manifest.Exhaustive = !sampled
	pa342Save(t, f)
	before := pa342Tree(t, f.root)
	s := pa315Prepare(t, f.p, pa318Options(t, real, new(int)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	began := time.Now()
	x, err := s.Run(ctx, pa318Driver(t, s).Key)
	if len(elapsed) != 0 {
		*elapsed[0] = time.Since(began)
	}
	if x == nil || (err != nil && x.Success) {
		t.Fatalf("execution: %+v %v", x, err)
	}
	if !reflect.DeepEqual(before, pa342Tree(t, f.root)) {
		t.Fatal("execution changed original application")
	}
	return x
}

func hold393Assert(t *testing.T, x *PythonApplicationExecutionResult) {
	t.Helper()
	if !x.Success || !x.Complete || x.Exploration == nil || x.ObservationMode != "explored" {
		t.Fatalf("held exploration did not complete: %+v", x)
	}
	e := x.Exploration
	if e.Runs != 9 || e.States != 3 || e.Depth != 3 || e.Attempts != 9 || e.Refusals != 3 || e.Omitted != 0 || e.DeclaredRefusals == nil || *e.DeclaredRefusals != 1 || len(x.Observations) != 27 {
		t.Fatalf("wrong attempts, cleanup rows or states: %+v, rows=%d", e, len(x.Observations))
	}
	queued, released, busy, empty, readHeld, replayHold, replayRelease := 0, 0, 0, 0, 0, 0, 0
	for _, row := range x.Observations {
		wire := refusal392Wire(t, row)
		if row.Kind == "initial" {
			if wire["operation"] != nil || row.State["count"] != json.Number("0") || row.State["active"] != json.Number("0") || row.State["prepared"] != false {
				t.Fatalf("cleanup contaminated next run: %v", wire)
			}
			continue
		}
		if row.Kind != "transition" || row.Called == nil || row.Replay == nil {
			t.Fatalf("unexpected cleanup/transition row: %v", wire)
		}
		isBusy := row.Before["active"] == json.Number("1")
		switch row.Step {
		case "Submit":
			if wire["operation"] != "hold" || row.Entry != pa342ID(pa342Source, "submit") || *row.Called == isBusy {
				t.Fatalf("hold did not count exactly its new start: %v", wire)
			}
			if isBusy {
				if row.Outcome != "refused" || row.Refusal == nil || row.Refusal.Type != "counter.core.Refused" || row.Refusal.Attributes["status_code"] != json.Number("409") || !reflect.DeepEqual(row.Before, row.After) {
					t.Fatalf("busy Submit lost declared refusal: %v", wire)
				}
				if !*row.Replay {
					busy++
				}
			} else {
				want := map[string]any{"count": json.Number("1"), "active": json.Number("1"), "prepared": true}
				if row.Outcome != "accepted" || !reflect.DeepEqual(row.After, want) {
					t.Fatalf("target ran before hold, or other entry work was not drained: %v", wire)
				}
				if *row.Replay {
					replayHold++
				} else {
					queued++
				}
			}
		case "Finish":
			if wire["operation"] != "release" || *row.Called || row.Entry != row.Target || row.EntrySHA256 != row.SourceSHA256 || row.Outcome != "" || row.Refusal != nil {
				t.Fatalf("release invented an entry or another call: %v", wire)
			}
			if isBusy {
				want := map[string]any{"count": json.Number("2"), "active": json.Number("0"), "prepared": true}
				if !reflect.DeepEqual(row.After, want) {
					t.Fatalf("release did not drain descendants: %v", wire)
				}
				if *row.Replay {
					replayRelease++
				} else {
					released++
				}
			} else {
				if !reflect.DeepEqual(row.Before, row.After) {
					t.Fatalf("empty release changed state: %v", wire)
				}
				if !*row.Replay {
					empty++
				}
			}
		case "Read":
			if wire["operation"] != nil || *row.Called || row.Outcome != "accepted" || !reflect.DeepEqual(row.Before, row.After) {
				t.Fatalf("ordinary step changed while held: %v", wire)
			}
			if isBusy && !*row.Replay {
				readHeld++
			}
		default:
			t.Fatalf("model step renamed: %v", wire)
		}
	}
	if queued != 2 || released != 1 || busy != 1 || empty != 2 || readHeld != 1 || replayHold == 0 || replayRelease == 0 {
		t.Fatalf("missing hold/release/refusal/replay: %d %d %d %d %d %d %d", queued, released, busy, empty, readHeld, replayHold, replayRelease)
	}
	b, _ := json.Marshal(x)
	var back PythonApplicationExecutionResult
	if err := json.Unmarshal(b, &back); err != nil || !reflect.DeepEqual(refusal392Wire(t, x), refusal392Wire(t, back)) {
		t.Fatalf("hold evidence lost on JSON round trip: %v", err)
	}
	raw, err := pythonApplicationAttempts(*x)
	if err != nil {
		t.Fatal(err)
	}
	var attempts struct {
		States   []map[string]any `json:"states"`
		Attempts [][]any          `json:"attempts"`
	}
	if err := json.Unmarshal(raw, &attempts); err != nil || len(attempts.States) != 3 || len(attempts.Attempts) != 9 {
		t.Fatalf("hold/release attempts lost: %s %v", raw, err)
	}
}

func TestPythonApplicationExecutionHeldCalls(t *testing.T) {
	hold393API(t)
	refusal392Modes(t, func(t *testing.T, real bool) {
		x := hold393Execution(t, real, hold393Source, hold393Driver, false)
		hold393Assert(t, x)
		// A second staged execution must reproduce portable observations,
		// including which operations were replays; no timing controls the pause.
		y := hold393Execution(t, real, hold393Source, hold393Driver, false)
		hold393Assert(t, y)
		if !reflect.DeepEqual(refusal392Wire(t, x), refusal392Wire(t, y)) {
			t.Fatal("relocation/repetition changed held evidence")
		}
		// A synchronous entry and a selected call started by its descendant
		// obey the same ownership and pause rules as the async entry.
		for name, source := range map[string]string{
			"synchronous entry":          strings.ReplaceAll(strings.Replace(hold393Source, "async def submit", "def submit", 1), "    await asyncio.sleep(0)\n", ""),
			"descendant selected worker": strings.Replace(hold393Source, "target=increment, args=(value,)", "target=spawn, args=(value,)", 1) + "\ndef spawn(value):\n    threading.Thread(target=increment, args=(value,)).start()\n",
			"async selected worker":      strings.Replace(strings.Replace(hold393Source, "def increment(value):", "async def increment(value):\n    await asyncio.sleep(0)", 1), "target=increment, args=(value,)", "target=run_target, args=(value,)", 1) + "\ndef run_target(value):\n    asyncio.run(increment(value))\n",
		} {
			t.Run(name, func(t *testing.T) { hold393Assert(t, hold393Execution(t, real, source, hold393Driver, false)) })
		}
	})
}

func TestPythonApplicationExecutionHeldCallFailures(t *testing.T) {
	hold393API(t)
	refusal392Modes(t, func(t *testing.T, real bool) {
		hold393Assert(t, hold393Execution(t, real, hold393Source, hold393Driver, false))
		for _, c := range []struct {
			name, source, driver, diagnostic string
			sampled                          bool
		}{
			{name: "target on entry stack", source: strings.Replace(hold393Source, "threading.Thread(target=increment, args=(value,)).start()", "increment(value)", 1)},
			{name: "selected call never starts", source: strings.Replace(hold393Source, "threading.Thread(target=increment, args=(value,)).start()", "pass", 1)},
			{name: "call completed without gate pause", source: strings.Replace(hold393Source, "target=increment, args=(value,)", "target=untraced, args=(value,)", 1) + "\ndef untraced(value):\n    import sys\n    sys.settrace(None)\n    increment(value)\n"},
			{name: "second Submit starts a call", source: strings.Replace(hold393Source, "if counts['active'] != 0:", "if False:", 1), diagnostic: "second call"},
			{name: "ordinary step starts second call", source: hold393Source + "\ndef second():\n    threading.Thread(target=increment, args=(1,)).start()\n", driver: strings.Replace(hold393Driver, "life.step(app.read, expected_before=state, expected_after=state)", "life.step(app.second if state['active'] else app.read)", 1), diagnostic: "second call"},
			{name: "release deadline", source: strings.Replace(hold393Source, "    assert counts['value'] == 1", "    if audit['hang']:\n        threading.Event().wait()\n    assert counts['value'] == 1", 1), driver: strings.Replace(hold393Driver, "    life.release(", "    app.audit['hang'] = bool(state['active'])\n    life.release(", 1), diagnostic: "timed out"},
			{name: "cleanup deadline", source: strings.Replace(hold393Source, "    assert counts['value'] == 1", "    threading.Event().wait()\n    assert counts['value'] == 1", 1), diagnostic: "timed out"},
			{name: "sampled hold", sampled: true, driver: "from invariant_observe import module\nfrom counter import core as app\ndef drive():\n    with module(app.increment, fields={'count':'counts.value'}, timeout=2) as life:\n        life.hold(app.submit, 1)\nif __name__ == '__main__': drive()\n"},
			{name: "caught hold failure stays poisoned", source: strings.Replace(hold393Source, "threading.Thread(target=increment, args=(value,)).start()", "pass", 1), driver: strings.Replace(hold393Driver, "    life.hold(app.submit, value, hold='forwarded', expected_before=state, expected_after=expected)", "    try:\n        life.hold(app.submit, value)\n    except Exception:\n        pass", 1)},
			{name: "hold expectation cannot supply state", driver: strings.Replace(hold393Driver, "expected_after=expected", "expected_after={'count':2, 'active':0, 'prepared':True}", 1)},
			{name: "release expectation cannot supply state", driver: strings.Replace(hold393Driver, "life.release(expected_before=state, expected_after=expected)", "life.release(expected_before=state, expected_after={'count':3, 'active':1, 'prepared':True})", 1)},
			{name: "release cannot declare an application refusal", driver: strings.Replace(hold393Driver, "Step('Finish', finish)", "Step('Finish', finish, refusal=Refusal(app.Refused, status_code=409))", 1)},
			{name: "release retains target identity checks", driver: strings.Replace(hold393Driver, "    life.release(", "    if state['active']:\n        app.increment = app.read\n    life.release(", 1)},
		} {
			t.Run(c.name, func(t *testing.T) {
				if c.source == "" {
					c.source = hold393Source
				}
				if c.driver == "" {
					c.driver = hold393Driver
				}
				var elapsed time.Duration
				x := hold393Execution(t, real, c.source, c.driver, c.sampled, &elapsed)
				if x.Success || x.Complete || x.Exploration != nil || len(x.Observations) != 0 || len(x.Diagnostics) == 0 {
					t.Fatalf("invalid held run retained evidence: %+v", x)
				}
				text := strings.ToLower(strings.Join(x.Diagnostics, "\n"))
				if c.diagnostic != "" && !strings.Contains(text, c.diagnostic) {
					t.Fatalf("wrong failure: %s", text)
				}
				if c.diagnostic == "second call" && !strings.Contains(text, "held") {
					t.Fatalf("failure must explain held call: %s", text)
				}
				if c.diagnostic == "timed out" && elapsed > 25*time.Second {
					t.Fatal("independent watchdog did not return promptly")
				}
			})
		}
		for _, form := range []string{"functional", "instance"} {
			t.Run(form+" cannot hold", func(t *testing.T) {
				source := "class Counter:\n    def __init__(self): self.count = 0\n    def increment(self): self.count = 1\n    def submit(self): self.increment()\ndef initial(): return 0\ndef increment(count): return 1\ndef submit(count): return increment(count)\n"
				driver := "from invariant_observe import Step, explore, functional, instance\nfrom counter import core as app\ndef start():\n    return functional(app.increment, app.initial, argument='count', field='count')\ndef submit(life, state):\n    life.hold(app.submit)\ndef drive():\n    explore(start, [Step('Submit', submit)])\nif __name__ == '__main__': drive()\n"
				f := hold393Fixture(t, true, source, driver)
				if form == "instance" {
					driver = strings.Replace(driver, "def start():\n    return functional(app.increment, app.initial, argument='count', field='count')", "CURRENT = []\ndef start():\n    c = app.Counter()\n    CURRENT[:] = [c]\n    return instance(c.increment, fields={'count':'count'})", 1)
					driver = strings.Replace(driver, "life.hold(app.submit)", "life.hold(CURRENT[0].submit)", 1)
					pa342Write(t, f.root, pa342DriverPath, driver)
					f.p.Manifest.Components.Checks[0].Target.Symbol = "Counter.increment"
					pa342Save(t, f)
				}
				s := pa315Prepare(t, f.p, pa318Options(t, real, new(int)))
				x, _ := s.Run(pa321Context(t), pa318Driver(t, s).Key)
				if x == nil || x.Success || x.Complete || len(x.Observations) != 0 || len(x.Diagnostics) == 0 {
					t.Fatalf("non-module hold accepted: %+v", x)
				}
			})
		}
	})
}
