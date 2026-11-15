package verify

// Issue #935 is one complete plumbing change, completing parent #789.
//
// Public API fixed by this contract:
//
//   PythonApplicationSession.RunBugReplay(ctx, key, BugReplayRequest)
//       (*PythonApplicationExecutionResult, error)
//
// key is an existing conformance invocation, including legacy sample/explore;
// tests and foreign keys are refused. Each call owns a copy of the request,
// gives it a fresh invocation ID, and runs the same staged driver/argv/cwd/env,
// selected declarations, model generation and admitted image as Run. Validate
// the request identity, bug pin, proposal, model/includes and bounds against
// that session. No public API attaches supplied replay records as gate success.
//
// Python drivers use:
//   from invariant_observe import replay, Step, Refusal
//   def drive():
//       if replay(start, steps):
//           return
//       ... existing sample or explore ...
//
// replay returns False without executing start/take in an ordinary invocation;
// in bug-replay mode it runs the required path and returns True. A missing,
// repeated, caught-failed or unfinished replay call fails the invocation even
// if ordinary sample/explore completes. start returns one unentered lifecycle;
// a list of starts is supported as in explore, with actual initial read-back
// selecting the matching start and no fabricated state or omitted cleanup.
// Step names bind concrete path actions (including bug-only actions) to
// take(life, state, *args). Dispatch exact requested argument values in the
// existing Step encoding; do not use enumeration/sampling limits to omit a
// requested operation. No synthetic bug mutations or application source edits.
// Preserve each form's restrictions: functional/instance entries take their
// existing arguments, module entries forward parameters; unsupported values
// or bindings fail with a named replay diagnostic, never lossy conversion.
//
// The collector drives the prefix through the first offending call inclusive,
// not a random run or exploration replay. It authenticates request/path ID,
// fresh invocation, actual lexical driver/target/entry, source/read-back,
// action/arguments/order, initial/before/after state, called, operation and
// refusal metadata, cleanup and end. Go validates all of these independently.
// Dispatched in the common contract means the actual application entry was
// attempted (or the recorded hold was released), not that the selected target
// was necessarily entered: a guarded entry may refuse before that target.
// Preserve called=false for that case. An omitted take is never a refusal.
// Declared exceptions retain exact class/attribute matching and unchanged
// state, no target/thread start, owned stack/awaitable and poisoning rules.
// Undeclared normal unchanged returns retain the existing lifecycle refusal
// convention, subject to actual entry dispatch and the common TLC check;
// a declared normal read-only return remains accepted, not an exception refusal.
// Hold/release retain module-only ownership, starts/completion, continuity,
// drainage and deadlines; cleanup changes produce no extra model observations.
// No stream prefix survives invalid authentication, binding, process or cleanup.
// Keep the existing in-process trust boundary; do not claim hostile Python
// isolation or let public attribution/driver-authored JSON fabricate evidence.
//
// Successful raw replay execution has observation_mode "bug-replay", ordinary
// bound Observations plus bug_replay:{request,result,observation}. It has no
// Exploration or runtime Tests. Raw Success means execution completed, not that
// the bug was rejected: reproduction still has an actual completed transcript.
// RunBugReplay does not run TLC. The full gate checks it with CheckBugReplay,
// retaining a named failure if executable dispatch cannot be established.
//
// PythonApplicationReport adds bug_replays:[{key,evidence}], where evidence is
// the existing BugReplayEvidence, one per current-bug/conformance-key pair in
// deterministic bug and invocation order. Executions contains normal runs and
// actual replay runs; associate replay checks by key and request/invocation ID.
// Top-level Bugs[i].Replay aggregates all required pairs: use the first failed
// pair's diagnostic/transcript, or a completed pair when all pass, keep the
// original Counterexample, and never let a later pass hide an earlier failure.
// Missing/unextractable paths remain failed obligations with named diagnostics.
// All selected invocations are required, not just the first driver or target.
// The gate does not let Options, an exhaustive declaration, a smaller sampling
// budget or the absence of outDir disable replay. Model-only never executes it.
// Keep independently valid tests, model/conformance and proofs on failure;
// replay adds no proof coverage, random sample states, tried-step counts or
// larger-bound coverage, and is distinct from exploration's Replay field.
// The existing streaming recording conversion must reject bug-replay input.
//
// Stable identities cover all requests, operations, exact observations,
// refusal descriptors, results, model/includes/bounds, source/dependency and
// runner/collector identities. Remove invocation nonces, authentication data,
// temporary paths and local image/container IDs from portable fingerprints.
// JSON keeps exact integers, module list/record semantics and Step model-value
// distinctions. Preserve compatible old receipts and command adapter behavior.
//
// Builder regressions must exercise the same fixtures against real Python
// under INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL or the existing gate opt-in,
// and add integration-tagged real pinned-TLC tests in the planned integration
// file for both bugs, repaired code, missing calls, refusal mismatch, cleanup,
// and cancellation. No verdict-only double substitutes for those CI tests.
// Keep the local GateFailures/GateFingerprints scheduling in pythongate and
// their opt-in entry points unchanged. Preserve every existing authentication,
// declared-refusal, module, hold, continuity, process and streaming assertion.
// Only the named old availability and harness-warning tests change their
// superseded expectations. Writers must never reach into invariant_observe;
// reviewers report such tampering, since the collector shares the driver's
// process and review is the check against it (D-0483).
// CommandReplayAvailable remains false for existing Python; factory notices
// and agent guidance use the application adapter's availability instead of
// routing these projects into ordinary command replay. Writer/reviewer copies
// of invariant_observe.py come from the embedded collector, are never shipped
// as application code, and reviews include the selected application's actual
// surrounding entries at their original application-relative paths.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

type pa935ReplayAPI interface {
	RunBugReplay(context.Context, PythonApplicationInvocationKey, BugReplayRequest) (*PythonApplicationExecutionResult, error)
}

func pa935API(t *testing.T, s *PythonApplicationSession) pa935ReplayAPI {
	t.Helper()
	api, ok := any(s).(pa935ReplayAPI)
	if !ok {
		t.Fatal("prepared Python application sessions cannot execute required authenticated bug replays")
	}
	return api
}

const pa935Model = `---- MODULE Lifecycle ----
EXTENDS Naturals
CONSTANT Token
VARIABLE phase, deliveries, reused
vars == <<phase, deliveries, reused>>
Init == phase = 0 /\ deliveries = 0 /\ reused = FALSE
Deliver(id, name) == phase = 0 /\ id = Token /\ name = "job"
  /\ phase' = 1 /\ deliveries' = 1 /\ UNCHANGED reused
Reload == phase = 1 /\ phase' = 2 /\ UNCHANGED <<deliveries, reused>>
Recover == phase = 2 /\ phase' = 3 /\ UNCHANGED <<deliveries, reused>>
Idle == phase = 3 /\ UNCHANGED vars
Next == Deliver(Token, "job") \/ Reload \/ Recover \/ Idle
Spec == Init /\ [][Next]_vars
Safe == deliveries <= 1 /\ ~reused
ReplayTask(id, name) == phase = 3 /\ id = Token /\ name = "job"
  /\ deliveries' = 2 /\ UNCHANGED <<phase, reused>>
ReuseName(id, name) == phase = 3 /\ id = Token + 1 /\ name = "job"
  /\ deliveries' = 2 /\ reused' = TRUE /\ UNCHANGED phase
ReplayCompletedTask == ReplayTask(Token, "job")
ReuseRecoveredName == ReuseName(Token + 1, "job")
====
`

// Recovery forgets either completed IDs or reserved names. The same real
// application entry handles initial delivery and either forbidden redelivery.
// No application function knows about a model, replay or the collector.
const pa935Source = `state = {'phase': 0, 'deliveries': 0, 'reused': False}
disk_ids = set()
disk_names = set()
ids = set()
names = set()
class Refused(Exception):
    def __init__(self, code):
        self.code = code
class Child(Refused):
    pass
def reset():
    state.update(phase=0, deliveries=0, reused=False)
    disk_ids.clear()
    disk_names.clear()
    ids.clear()
    names.clear()
def advance(phase, deliveries, reused):
    state.update(phase=phase, deliveries=deliveries, reused=reused)
def deliver(identity, name):
    assert type(identity) is int and identity in (9007199254740993, 9007199254740994)
    assert name == 'job'
    if identity in ids or (name in names and identity not in disk_ids):
        raise Refused(409)
    if state['phase'] == 2:
        raise Refused(409)
    reused = name in disk_names and identity not in disk_ids
    disk_ids.add(identity)
    disk_names.add(name)
    ids.add(identity)
    names.add(name)
    advance(1 if state['phase'] == 0 else state['phase'], state['deliveries']+1, reused)
def reload():
    if state['phase'] != 1:
        return
    ids.clear()
    names.clear()
    advance(2, state['deliveries'], state['reused'])
def recover():
    if state['phase'] != 2:
        return
    ids.update([] if __REPLAY_BUG__ else disk_ids)
    names.update([] if __REUSE_BUG__ else disk_names)
    advance(3, state['deliveries'], state['reused'])
def idle():
    advance(state['phase'], state['deliveries'], state['reused'])
def absent(identity, name):
    return None
def wrong(identity, name):
    state['deliveries'] += 1
`

const pa935Driver = `import random
from invariant_observe import module, Step, Refusal, replay, explore
from tasks import lifecycle as app
def start():
    app.reset()
    return module(app.advance, fields={'phase':'state.phase', 'deliveries':'state.deliveries', 'reused':'state.reused'}, timeout=10)
def deliver(life, state, identity, name):
    life.step(app.deliver, identity, name)
def reload(life, state):
    life.step(app.reload)
def recover(life, state):
    life.step(app.recover)
def idle(life, state):
    life.step(app.idle)
def drive():
    steps = [Step('Deliver', deliver, [[9007199254740993, 'job']], refusal=Refusal(app.Refused, code=409)), Step('Reload', reload), Step('Recover', recover), Step('Idle', idle)]
    bugs = [Step('ReplayTask', deliver, [[9007199254740993, 'job']], refusal=Refusal(app.Refused, code=409)),
            Step('ReuseName', deliver, [[9007199254740994, 'job']], refusal=Refusal(app.Refused, code=409))]
    if replay(start, steps + bugs):
        return
    with start() as life:
        entry, args = random.choice([(app.reload, ()), (app.deliver, (9007199254740993, 'job')), (app.recover, ()), (app.idle, ())])
        life.step(entry, *args)
if __name__ == '__main__':
    drive()
`

type pa935Raw struct {
	Request     BugReplayRequest      `json:"request"`
	Result      *BugReplayResult      `json:"result"`
	Observation *BugReplayObservation `json:"observation"`
}

type pa935Checked struct {
	Key      PythonApplicationInvocationKey `json:"key"`
	Evidence BugReplayEvidence              `json:"evidence"`
}

func pa935JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pa935RawOf(t *testing.T, x *PythonApplicationExecutionResult) pa935Raw {
	t.Helper()
	var wire struct {
		Replay *pa935Raw `json:"bug_replay"`
	}
	if err := json.Unmarshal(pa935JSON(t, x), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Replay == nil {
		t.Fatalf("execution lost its bug replay identity: %s", pa935JSON(t, x))
	}
	return *wire.Replay
}

func pa935CheckedOf(t *testing.T, r *Report) []pa935Checked {
	t.Helper()
	var wire struct {
		Rows []pa935Checked `json:"bug_replays"`
	}
	if err := json.Unmarshal(pa935JSON(t, r.Application), &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Rows
}

func pa935Fixture(t *testing.T, explicit bool, mode, driver string) (string, *project.Project) {
	t.Helper()
	if driver == "" {
		driver = pa935Driver
	}
	source := strings.ReplaceAll(pa935Source, "__REPLAY_BUG__", map[bool]string{true: "True", false: "False"}[mode == "replay" || mode == "both"])
	source = strings.ReplaceAll(source, "__REUSE_BUG__", map[bool]string{true: "True", false: "False"}[mode == "reuse" || mode == "both"])
	if mode == "alternative" {
		source = strings.Replace(source, "raise Refused(409)", "advance(state['phase'], state['deliveries'], state['reused'])\n        return", 1)
	}
	root, p := pa321Fixture(t, source, driver)
	p.Manifest.Components.Checks[0].Invocations[1].Sampling = map[string]string{"runs": "1", "steps": "1", "seed": "1"}
	p.Manifest.Components.Checks[0].Invocations[1].Inputs = []string{pa315Project + "/.invariant/specs/Lifecycle.tla", pa315Project + "/.invariant/specs/Local.tla", pa315Project + "/.invariant/ratified.lock"}
	if !explicit {
		p.Manifest.Components = nil
	}
	pa310Write(t, root, "tests/test_tasks.py", "import unittest\nfrom tasks import lifecycle as app\nclass TaskTests(unittest.TestCase):\n    def test_pass(self):\n        app.deliver(9007199254740993, 'job')\n        self.assertEqual(app.state['deliveries'], 1)\n")
	pa310Write(t, p.Dir, p.Manifest.Module, pa935Model)
	model, err := tla.Read(pa935Model, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Lock = project.Lock{Bounds: map[string]string{"Token": "9007199254740993"}}
	for _, statement := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant},
		{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"},
		{Name: "ReuseRecoveredName", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"}} {
		statement.SHA256, err = model.PinHash(statement.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		statement.Says = statement.Name
		p.Lock.Statements = append(p.Lock.Statements, statement)
	}
	p.Lock.Ratified = &project.Ratification{By: "fixture", Issue: 935, Proposal: project.ProposalHash(p.Lock.Bounds, p.Lock.Statements)}
	pa310Save(t, p)
	return root, p
}

func pa935State(phase, delivered int, reused bool) tlc.ReplayState {
	return tlc.ReplayState{"phase": {Kind: "integer", Atom: fmt.Sprint(phase)}, "deliveries": {Kind: "integer", Atom: fmt.Sprint(delivered)}, "reused": {Kind: "bool", Atom: map[bool]string{true: "TRUE", false: "FALSE"}[reused]}}
}

func pa935Request(t *testing.T, p *project.Project, second bool) BugReplayRequest {
	t.Helper()
	bug := p.Lock.Statements[2]
	action, id := "ReplayTask", "9007199254740993"
	if second {
		bug, action, id = p.Lock.Statements[3], "ReuseName", "9007199254740994"
	}
	args := func(id string) []tlc.ReplayValue {
		return []tlc.ReplayValue{{Kind: "integer", Atom: id}, {Kind: "string", Atom: "job"}}
	}
	path := tlc.ReplayPath{Version: 1, Module: p.ModuleName(), Bug: bug.Name, BugPin: bug.SHA256, Expect: "Safe", ExpectKind: "invariant", Specification: "Spec",
		ModelSHA256: pythonApplicationHash([]byte(pa935Model)), IncludesSHA256: map[string]string{"Local.tla": pythonApplicationHash([]byte("---- MODULE Local ----\nHelper == TRUE\n====\n"))}, Bounds: p.Lock.Bounds,
		Tool: tlc.ReplayTool{Image: toolchain.JavaImage, JarSHA256: pythonApplicationHash([]byte("fixture jar"))}, Offending: 3,
		States: []tlc.ReplayState{pa935State(0, 0, false), pa935State(1, 1, false), pa935State(2, 1, false), pa935State(3, 1, false), pa935State(3, 2, second)},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Deliver", Args: args("9007199254740993")}, {From: 1, To: 2, Action: "Reload"}, {From: 2, To: 3, Action: "Recover"}, {From: 3, To: 4, Action: action, Args: args(id), Bug: true}}}
	// The path is a direct-session fixture, never claimed to have run TLC.
	// The gate test below obtains its paths through production TLC extraction.
	path.ID = pythonApplicationHash(pa935JSON(t, path))
	q, err := NewBugReplayRequest(bug, project.ProposalHash(p.Lock.Bounds, p.Lock.Statements), path)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func pa935Run(t *testing.T, s *PythonApplicationSession, q BugReplayRequest) *PythonApplicationExecutionResult {
	t.Helper()
	x, err := pa935API(t, s).RunBugReplay(pa321Context(t), pa318Driver(t, s).Key, q)
	if err != nil || x == nil || !x.Success || !x.Complete || x.ExitCode != 0 {
		t.Fatalf("authenticated replay did not finish: %+v, %v", x, err)
	}
	if x.ObservationMode != "bug-replay" || x.Exploration != nil || len(x.Tests) != 0 {
		t.Fatalf("replay mislabeled as ordinary execution: %+v", x)
	}
	w := pa935RawOf(t, x)
	if w.Request.ID != q.ID || w.Request.InvocationID == "" || w.Result == nil || w.Observation == nil || !w.Observation.Started || !w.Observation.Completed || w.Observation.ExitCode != 0 {
		t.Fatalf("incomplete replay record: %+v", w)
	}
	if w.Result.RequestID != q.ID || w.Observation.RequestID != q.ID || w.Result.InvocationID != w.Request.InvocationID || w.Observation.InvocationID != w.Request.InvocationID || len(w.Result.Calls) != q.Path.Offending+1 || !reflect.DeepEqual(w.Result.Calls, w.Observation.Calls) {
		t.Fatalf("crossed identities/calls: %+v", w)
	}
	for i, c := range w.Result.Calls {
		step := q.Path.Steps[i]
		if c.Index != i || c.Action != step.Action || !sameReplayValues(c.Args, step.Args) || !c.Dispatched || c.Error != "" || !c.Before.Equal(q.Path.States[i]) {
			t.Fatalf("not the requested actual call: %+v", c)
		}
		if i < q.Path.Offending && !c.After.Equal(q.Path.States[i+1]) {
			t.Fatalf("prefix changed: %+v", c)
		}
	}
	return x
}

func TestPythonApplicationBugReplayLifecycles935(t *testing.T) {
	pa935API(t, &PythonApplicationSession{})
	for _, explicit := range []bool{true, false} {
		for _, mode := range []string{"fixed", "replay", "reuse", "both"} {
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, mode), func(t *testing.T) {
				root, p := pa935Fixture(t, explicit, mode, "")
				before := pa315Tree(t, root)
				selected := 0
				s := pa315Prepare(t, p, pa318Options(t, false, &selected))
				sample, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
				if err != nil || !sample.Success || sample.ObservationMode != "sampled" || len(sample.Observations) != 2 {
					t.Fatalf("sample that misses recovery must finish: %+v %v", sample, err)
				}
				for _, second := range []bool{false, true} {
					q := pa935Request(t, p, second)
					x := pa935Run(t, s, q)
					w := pa935RawOf(t, x)
					last := w.Result.Calls[3]
					broken := mode == "both" || mode == "replay" && !second || mode == "reuse" && second
					if broken != last.After.Equal(q.Path.States[4]) || (last.Outcome == "refused") == broken {
						t.Fatalf("wrong actual redelivery result: %+v", w)
					}
					if len(x.Observations) != 5 {
						t.Fatalf("lost initial/prefix/offending observations: %+v", x)
					}
					for i, row := range x.Observations {
						if row.Form != "module" || row.Key != x.Key || row.DriverSHA256 == "" || row.SourceSHA256 == "" || row.EntrySHA256 == "" || row.Readback["deliveries"] != "state.deliveries" || row.Sequence != i {
							t.Fatalf("unbound lifecycle row: %+v", row)
						}
					}
					if !broken {
						row := x.Observations[4]
						if row.Called == nil || *row.Called || row.Outcome != "refused" || row.Refusal == nil || row.Refusal.Attributes["code"] != json.Number("409") {
							t.Fatalf("declared guard refusal lost: %+v", row)
						}
					}
				}
				if selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
					t.Fatal("replay restaged mutable inputs or reselected dependencies")
				}
			})
		}
	}
	pa935ScalarForms(t)
	pa935ModuleValues(t)
}

func pa935ModuleValues(t *testing.T) {
	t.Helper()
	root, p := pa935Fixture(t, true, "fixed", "")
	q := pa935Request(t, p, false)
	value, err := tlc.ParseReplayValue(`[tokens |-> <<9007199254740993>>, flag |-> TRUE]`)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(pa935Model, "CONSTANT Token", "CONSTANT Token, Job", 1)
	body = strings.Replace(body, "VARIABLE phase, deliveries, reused", "VARIABLE phase, deliveries, reused, details", 1)
	body = strings.ReplaceAll(body, "<<phase, deliveries, reused>>", "<<phase, deliveries, reused, details>>")
	body = strings.Replace(body, "Init == phase = 0 /\\ deliveries = 0 /\\ reused = FALSE", "Init == phase = 0 /\\ deliveries = 0 /\\ reused = FALSE /\\ details = [tokens |-> <<9007199254740993>>, flag |-> TRUE]", 1)
	body = strings.ReplaceAll(body, "UNCHANGED reused", "UNCHANGED <<reused, details>>")
	body = strings.ReplaceAll(body, "UNCHANGED <<deliveries, reused>>", "UNCHANGED <<deliveries, reused, details>>")
	body = strings.ReplaceAll(body, "UNCHANGED <<phase, reused>>", "UNCHANGED <<phase, reused, details>>")
	body = strings.ReplaceAll(body, "UNCHANGED phase", "UNCHANGED <<phase, details>>")
	body = strings.ReplaceAll(body, `"job"`, "Job")
	p.Lock.Bounds["Job"] = "JobName"
	for i := range q.Path.States {
		q.Path.States[i]["details"] = value
	}
	for i := range q.Path.Steps {
		if len(q.Path.Steps[i].Args) == 2 {
			q.Path.Steps[i].Args[1] = tlc.ReplayValue{Kind: "model", Atom: "JobName"}
		}
	}
	q = pa935Rebind(t, p, q.Path, body)
	b, err := os.ReadFile(filepath.Join(root, pa315Target))
	if err != nil {
		t.Fatal(err)
	}
	pa310Write(t, root, pa315Target, "meta = {'tokens':[9007199254740993], 'flag':True}\n"+string(b))
	driver := strings.Replace(pa935Driver, "'reused':'state.reused'", "'reused':'state.reused', 'details':'meta'", 1)
	driver = strings.ReplaceAll(driver, "'job']", "{'$mv':'JobName'}]")
	driver = strings.Replace(driver, "    life.step(app.deliver, identity, name)", "    assert name == {'$mv':'JobName'}\n    life.step(app.deliver, identity, 'job')", 1)
	pa310Write(t, root, pa315Project+"/driver.py", driver)
	x := pa935Run(t, pa315Prepare(t, p, pa318Options(t, false, new(int))), q)
	w := pa935RawOf(t, x)
	if !w.Result.Initial["details"].Equal(value) || w.Result.Calls[3].Args[1].Kind != "model" || w.Result.Calls[3].Args[1].Atom != "JobName" {
		t.Fatalf("module or model value changed meaning: %+v", w)
	}
	var round PythonApplicationExecutionResult
	if err := json.Unmarshal(pa935JSON(t, x), &round); err != nil {
		t.Fatal(err)
	}
	if pythonApplicationExecutionFingerprint(round) != x.Fingerprint || !pa935RawOf(t, &round).Result.Initial["details"].Equal(value) {
		t.Fatal("exact nested module state did not survive JSON")
	}
}

func pa935Rebind(t *testing.T, p *project.Project, path tlc.ReplayPath, body string) BugReplayRequest {
	t.Helper()
	pa310Write(t, p.Dir, p.Manifest.Module, body)
	includes, err := tla.Siblings(filepath.Dir(p.ModulePath()), filepath.Base(p.ModulePath()))
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Lock.Statements {
		s := &p.Lock.Statements[i]
		s.SHA256, err = tla.PinHashWith(body, includes, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	p.Lock.Ratified = &project.Ratification{By: "fixture", Issue: 935, Proposal: project.ProposalHash(p.Lock.Bounds, p.Lock.Statements)}
	pa310Save(t, p)
	path.ID, path.ModelSHA256, path.Bounds, path.Module = "", pythonApplicationHash([]byte(body)), p.Lock.Bounds, p.ModuleName()
	path.IncludesSHA256 = map[string]string{}
	for name, src := range includes {
		path.IncludesSHA256[name] = pythonApplicationHash([]byte(src))
	}
	var bug project.Statement
	for _, s := range p.Lock.Statements {
		if s.Name == path.Bug {
			bug = s
		}
	}
	path.BugPin = bug.SHA256
	path.ID = pythonApplicationHash(pa935JSON(t, path))
	q, err := NewBugReplayRequest(bug, project.ProposalHash(p.Lock.Bounds, p.Lock.Statements), path)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func pa935ScalarForms(t *testing.T) {
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
				s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
				x := pa935Run(t, s, q)
				w := pa935RawOf(t, x)
				if w.Result.Outcome != "refused" || len(x.Observations) != 3 || x.Observations[2].Refusal == nil {
					t.Fatalf("scalar form did not record actual guard refusal: %+v", x)
				}
			})
		}
	}
}

func pa935Rejected(t *testing.T, x *PythonApplicationExecutionResult, err error) {
	t.Helper()
	if x != nil && (x.Success || x.Complete || len(x.Observations) != 0) {
		t.Fatalf("invalid replay retained accepted execution: %+v %v", x, err)
	}
	if x != nil {
		var wire struct {
			Replay *pa935Raw `json:"bug_replay"`
		}
		if e := json.Unmarshal(pa935JSON(t, x), &wire); e != nil {
			t.Fatal(e)
		}
		if wire.Replay != nil && wire.Replay.Observation != nil && wire.Replay.Observation.Completed {
			t.Fatalf("invalid replay retained completed transcript: %+v", wire.Replay)
		}
	}
	if err == nil && (x == nil || len(x.Diagnostics) == 0 && x.HarnessError == "") {
		t.Fatal("failed replay has no named diagnostic")
	}
}

func TestPythonApplicationBugReplayRejectsMissingAndInvalidCalls935(t *testing.T) {
	pa935API(t, &PythonApplicationSession{})
	_, p := pa935Fixture(t, true, "fixed", "")
	s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
	pa935Run(t, s, pa935Request(t, p, false))
	cases := []struct{ name, driver, from, to string }{
		{"missing replay but successful sampling", strings.Replace(pa935Driver, "    if replay(start, steps + bugs):\n        return\n", "", 1), "", ""},
		{"omitted offending dispatch", strings.Replace(pa935Driver, "    life.step(app.deliver, identity, name)", "    if state['phase'] == 3: return\n    life.step(app.deliver, identity, name)", 1), "", ""},
		{"missing action", strings.Replace(pa935Driver, "Step('ReplayTask'", "Step('OtherAction'", 1), "", ""},
		{"missing actual selected call", "", "    advance(2, state['deliveries'], state['reused'])", "    pass"},
		{"wrong source target", strings.Replace(pa935Driver, "module(app.advance,", "module(app.absent,", 1), "", ""},
		{"direct target as entry", strings.Replace(pa935Driver, "life.step(app.reload)", "life.step(app.advance, 2, 1, False)", 1), "", ""},
		{"wrong driver function", strings.Replace(pa935Driver, "def drive():", "def other():", 1) + "\n", "", ""},
		{"wrong initial state", "", "state.update(phase=0, deliveries=0, reused=False)", "state.update(phase=7, deliveries=0, reused=False)"},
		{"wrong prefix readback", "", "advance(2, state['deliveries']", "advance(9, state['deliveries']"},
		{"state mutation between calls", strings.Replace(pa935Driver, "    life.step(app.recover)", "    app.state['phase'] = 0\n    life.step(app.recover)", 1), "", ""},
		{"arbitrary exception", "", "raise Refused(409)", "raise ValueError('not a refusal')"},
		{"subclass exception", "", "raise Refused(409)", "raise Child(409)"},
		{"wrong exception attribute", "", "raise Refused(409)", "raise Refused(410)"},
		{"float exception attribute", "", "raise Refused(409)", "raise Refused(409.0)"},
		{"target called before refusal", "", "raise Refused(409)", "advance(state['phase'], state['deliveries'], state['reused'])\n        raise Refused(409)"},
		{"early exit zero", "", "def recover():", "def recover():\n    import os\n    os._exit(0)"},
		{"driver catches missing take", strings.Replace(pa935Driver, "    if replay(start, steps + bugs):\n        return", "    try:\n        if replay(start, []): return\n    except Exception:\n        pass", 1), "", ""},
		{"double replay", strings.Replace(pa935Driver, "    if replay(start, steps + bugs):\n        return", "    if replay(start, steps + bugs):\n        replay(start, steps + bugs)\n        return", 1), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, p := pa935Fixture(t, true, "fixed", c.driver)
			if c.from != "" {
				b, e := os.ReadFile(filepath.Join(root, pa315Target))
				if e != nil {
					t.Fatal(e)
				}
				pa310Write(t, root, pa315Target, strings.Replace(string(b), c.from, c.to, 1))
			}
			if c.name == "wrong driver function" {
				pa310Write(t, root, pa315Project+"/driver.py", strings.Replace(c.driver, "    drive()", "    other()", 1)+"\ndef drive():\n    pass\n")
			}
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			x, e := pa935API(t, s).RunBugReplay(pa321Context(t), pa318Driver(t, s).Key, pa935Request(t, p, false))
			pa935Rejected(t, x, e)
		})
	}
	q := pa935Request(t, p, false)
	for _, key := range []PythonApplicationInvocationKey{pa315Unit(t, s).Key, {Target: "foreign", Check: "other", Kind: "conformance", ID: "fake"}} {
		x, e := pa935API(t, s).RunBugReplay(pa321Context(t), key, q)
		pa935Rejected(t, x, e)
	}
	for _, mutation := range []string{"request identity", "model", "bounds", "proposal", "statement"} {
		t.Run("stale "+mutation, func(t *testing.T) {
			q := pa935Request(t, p, false)
			switch mutation {
			case "request identity":
				q.ID = "other"
			case "model":
				q.Path.ModelSHA256 = pythonApplicationHash([]byte("other model"))
			case "bounds":
				q.Path.Bounds["Token"] = "7"
			case "proposal":
				q.Proposal = pythonApplicationHash([]byte("other proposal"))
			case "statement":
				q.Bug.Says = "a different ratified statement"
			}
			if mutation != "request identity" {
				q.Path.ID = ""
				q.Path.ID = pythonApplicationHash(pa935JSON(t, q.Path))
				q.ID = q.identity()
			}
			x, err := pa935API(t, s).RunBugReplay(pa321Context(t), pa318Driver(t, s).Key, q)
			pa935Rejected(t, x, err)
		})
	}
}

func pa935Resign(t *testing.T, events, secret []byte, mutate func([]map[string]any)) []byte {
	t.Helper()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(events), []byte{'\n'}) {
		var row map[string]any
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		if err := d.Decode(&row); err != nil {
			t.Fatal(err)
		}
		if row["type"] != "authentication" {
			rows = append(rows, row)
		}
	}
	mutate(rows)
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body.Bytes())
	if err := enc.Encode(map[string]string{"type": "authentication", "hmac_sha256": hex.EncodeToString(mac.Sum(nil))}); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestPythonApplicationBugReplayAuthenticatesAndCleansUp935(t *testing.T) {
	pa935API(t, &PythonApplicationSession{})
	for _, mode := range []string{"repeat stream", "other bug stream", "unsigned mutation", "signed wrong execution", "signed wrong key", "signed source hash", "signed wrong action", "signed wrong order", "signed wrong state", "missing end", "process exit", "cleanup failure", "cancel", "snapshot", "request"} {
		t.Run(mode, func(t *testing.T) {
			root, p := pa935Fixture(t, true, "fixed", "")
			before := pa315Tree(t, root)
			selected := 0
			opts := pa318Options(t, false, &selected)
			execute := opts.Execute
			var previous []byte
			var commands []PythonApplicationCommand
			calls := 0
			ctx, cancel := context.WithCancel(pa321Context(t))
			defer cancel()
			opts.Execute = func(cctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				calls++
				commands = append(commands, c)
				secret, e := os.ReadFile(filepath.Join(c.OutputDir, "authentication.key"))
				if e != nil {
					t.Fatal(e)
				}
				x, e := execute(cctx, c)
				if calls == 1 {
					previous = bytes.Clone(x.Events)
					return x, e
				}
				switch mode {
				case "repeat stream", "other bug stream":
					x.Events = bytes.Clone(previous)
				case "unsigned mutation":
					x.Events = bytes.Replace(x.Events, []byte(`"phase": 3`), []byte(`"phase": 9`), 1)
					if bytes.Equal(x.Events, previous) {
						t.Fatal("no fresh stream")
					}
					x.Events[0] = '['
				case "process exit":
					x.ExitCode = 7
				case "cleanup failure":
					e = errors.New("fixture cleanup failed after process exit")
				case "cancel":
					cancel()
					e = ctx.Err()
				case "snapshot":
					pa310Write(t, c.Root, "resources/bytes.bin", "changed staged resource")
				case "request":
					pa310Write(t, c.RuntimeDir, "request.json", "{}")
				default:
					x.Events = pa935Resign(t, x.Events, secret, func(rows []map[string]any) {
						for _, row := range rows {
							if row["type"] == "begin" {
								if mode == "signed wrong execution" {
									row["execution"] = "stale"
									return
								}
								if mode == "signed wrong key" {
									row["key"].(map[string]any)["id"] = "other"
									return
								}
							}
							if row["kind"] == "transition" {
								switch mode {
								case "signed source hash":
									row["source_sha256"] = "sha256:wrong"
									return
								case "signed wrong action":
									row["step"] = "OtherAction"
									return
								case "signed wrong order":
									row["sequence"] = json.Number("9")
									return
								case "signed wrong state":
									row["before"].(map[string]any)["phase"] = json.Number("9")
									return
								}
							}
							if row["type"] == "end" && mode == "missing end" {
								row["type"] = "unfinished"
								return
							}
						}
						t.Fatal("no authenticated row to corrupt")
					})
				}
				return x, e
			}
			s := pa315Prepare(t, p, opts)
			q := pa935Request(t, p, false)
			pa935Run(t, s, q)
			if mode == "other bug stream" {
				q = pa935Request(t, p, true)
			}
			x, e := pa935API(t, s).RunBugReplay(ctx, pa318Driver(t, s).Key, q)
			pa935Rejected(t, x, e)
			if mode == "cancel" && !errors.Is(e, context.Canceled) {
				t.Fatalf("cancellation lost: %v", e)
			}
			if calls != 2 || selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("wrong execution accounting or changed original")
			}
			for _, c := range commands {
				if _, e := os.Stat(c.OutputDir); !os.IsNotExist(e) {
					t.Fatalf("output survived failed replay: %v", e)
				}
			}
		})
	}
	pa935Interrupted(t)
}

func pa935Interrupted(t *testing.T) {
	t.Helper()
	for _, mode := range []string{"cancel running entry", "module deadline"} {
		t.Run(mode, func(t *testing.T) {
			driver := pa935Driver
			if mode == "module deadline" {
				driver = strings.Replace(driver, "timeout=10", "timeout=0.5", 1)
			}
			root, p := pa935Fixture(t, true, "fixed", driver)
			b, err := os.ReadFile(filepath.Join(root, pa315Target))
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Replace(string(b), "def recover():", "def recover():\n    import os, time\n    from pathlib import Path\n    Path(os.environ['APP_SIGNAL']).write_text('entered')\n    time.sleep(60)", 1)
			pa310Write(t, root, pa315Target, body)
			opts := pa318Options(t, false, new(int))
			execute := opts.Execute
			ctx, cancel := context.WithCancel(pa321Context(t))
			defer cancel()
			var command PythonApplicationCommand
			reached := make(chan struct{})
			opts.Execute = func(cctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				command = c
				// Translate this fixture-only output path as pa318Local translates
				// the production request and mount paths; it is never evidence.
				c.Env["APP_SIGNAL"] = filepath.Join(c.OutputDir, "entry-ready")
				if mode == "cancel running entry" {
					go func() {
						defer close(reached)
						for {
							if _, err := os.Stat(filepath.Join(c.OutputDir, "entry-ready")); err == nil {
								cancel()
								return
							}
							select {
							case <-ctx.Done():
								return
							case <-time.After(5 * time.Millisecond):
							}
						}
					}()
				}
				x, err := execute(cctx, c)
				if _, e := os.Stat(filepath.Join(c.OutputDir, "entry-ready")); e != nil {
					t.Errorf("did not reach real application call: %v", e)
				}
				return x, err
			}
			s := pa315Prepare(t, p, opts)
			began := time.Now()
			x, err := pa935API(t, s).RunBugReplay(ctx, pa318Driver(t, s).Key, pa935Request(t, p, false))
			pa935Rejected(t, x, err)
			if mode == "cancel running entry" {
				cancel()
				<-reached
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			}
			if time.Since(began) > 30*time.Second {
				t.Fatal("failed replay waited for blocked application work")
			}
			if _, e := os.Stat(command.OutputDir); !os.IsNotExist(e) {
				t.Fatalf("interrupted replay leaked output: %v", e)
			}
		})
	}
}

func TestPythonApplicationBugReplayHeldCalls935(t *testing.T) {
	pa935API(t, &PythonApplicationSession{})
	driver := strings.Replace(hold393Driver, "Refusal, Step, explore, module", "Refusal, Step, explore, module, replay", 1)
	driver = strings.Replace(driver, "    explore(start, [Step", "    if replay(start, [Step('Submit', submit, [[1]], refusal=Refusal(app.Refused, status_code=409)), Step('Finish', finish), Step('Busy', submit, [[1]], refusal=Refusal(app.Refused, status_code=409))]):\n        return\n    explore(start, [Step", 1)
	f := hold393Fixture(t, true, hold393Source, driver)
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
	s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
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

// Only TLC/Docker transport is doubled. Python observations are always made
// by the production runner and collector over actual selected fixture code.
// As with the existing pythongatetest fake, this never emits Python evidence.
const pa935Docker = `import sys, pathlib, json, os
args = sys.argv[1:]
if args and args[0] == 'rm': sys.exit(0)
if args[:2] == ['image', 'inspect']:
    print('linux/amd64'); sys.exit(0)
if args and args[0] == 'info':
    print('{"NCPU":1,"MemTotal":8589934592}'); sys.exit(0)
assert 'tlc2.TLC' in args, ('only TLC is doubled', args)
assert '--network' in args and 'none' in args
mounts = {}
for i,a in enumerate(args[:-1]):
    if a == '-v':
        host, guest, *rest = args[i+1].split(':')
        mounts[guest] = host
module = pathlib.Path(args[-1]).stem
body = pathlib.Path(mounts['/work'], module + '.tla').read_text()
with open(os.environ['PA935_LOG'],'a') as log:
    log.write(json.dumps({'module':module,'body':body})+'\n')
def msg(code,text):
    print('@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@' % (code,text,code))
msg(2262,'TLC2 Version 2.19 of 08 August 2024')
if module.startswith('Bug_'):
    assert module in ('Bug_ReplayCompletedTask', 'Bug_ReuseRecoveredName'), module
    msg(2110,'Invariant Safe is violated.')
    for i in range(5):
        phase, deliveries = min(i,3), (0 if i==0 else 2 if i==4 else 1)
        reused = 'TRUE' if i==4 and module.endswith('ReuseRecoveredName') else 'FALSE'
        msg(2217,'%d: <opaque label>\n/\\ phase = %d\n/\\ deliveries = %d\n/\\ reused = %s' % (i+1,phase,deliveries,reused))
    msg(2186,'Finished.'); sys.exit(12)
if 'invariant-replay-v1' in body:
    config = pathlib.Path(mounts['/work'], 'Invariant.cfg').read_text()
    assert 'CONSTANT Token = 9007199254740993' in config
    assert 'Invariant_ReplayStates' in body
    second = 'Next \\/ ReuseRecoveredName' in body
    action, identity = ('ReuseName',9007199254740994) if second else ('ReplayTask',9007199254740993)
    print('<<"invariant-replay-v1", 0, {<<"Deliver", <<9007199254740993, "job">>, FALSE>>}>>')
    print('<<"invariant-replay-v1", 1, {<<"Reload", <<>>, FALSE>>}>>')
    print('<<"invariant-replay-v1", 2, {<<"Recover", <<>>, FALSE>>}>>')
    print('<<"invariant-replay-v1", 3, {<<"%s", <<%d, "job">>, TRUE>>}>>' % (action,identity))
if module == 'Invariant_ReplayResult':
    assert 'Invariant_ReplayAllowed' in body and 'phase |-> 3' in body
    if os.environ.get('PA935_ALTERNATIVE_FAIL') == '1' or 'deliveries |-> 2' in body or 'phase |-> 9' in body:
        msg(2110,'Invariant Invariant_ReplayAllowed is violated.')
        msg(2186,'Finished.'); sys.exit(12)
msg(2193,'Model checking completed. No error has been found.')
msg(2199,'5 states generated, 4 distinct states found, 0 states left on queue.')
msg(2194,'The depth of the complete state graph search is 4.')
msg(2186,'Finished.')
`

func pa935Tools(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	pa310Write(t, bin, "docker.py", pa935Docker)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	pa310Write(t, bin, "docker", "#!/bin/sh\nexec "+quote(python)+" "+quote(filepath.Join(bin, "docker.py"))+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0755); err != nil {
		t.Fatal(err)
	}
	pa310Write(t, bin, "fixture.jar", "fixture jar")
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PA935_LOG", log)
	t.Setenv("PA935_ALTERNATIVE_FAIL", "")
	return toolchain.Toolchain{JavaImage: toolchain.JavaImage, TLCJar: filepath.Join(bin, "fixture.jar"), PythonImage: toolchain.PythonImage}, log
}

func pa935Gate(t *testing.T, p *project.Project, tc toolchain.Toolchain) *Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := Run(ctx, p.Dir, "", tc, Options{TLCParallel: 1})
	if err != nil || r == nil {
		t.Fatalf("gate must return independent actual evidence: %+v %v", r, err)
	}
	return r
}

func pa935GateAssertions(t *testing.T, r *Report, explicit bool, mode string, invocations int) {
	t.Helper()
	if r.Application == nil || r.Passed != (mode == "fixed" || mode == "alternative") || !r.Design.Passed || !r.Build.Passed {
		t.Fatalf("wrong independent gate verdict: %s", pa935JSON(t, r))
	}
	rows := pa935CheckedOf(t, r)
	if len(rows) != 2*invocations {
		t.Fatalf("not every bug/invocation pair was checked: %s", pa935JSON(t, r.Application))
	}
	seen, nonces := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		e := row.Evidence
		if e.Request == nil || e.Result == nil || e.Observation == nil || !e.Required {
			t.Fatalf("no bound replay check: %+v", row)
		}
		pair := e.Request.Bug.Name + "/" + row.Key.ID
		if seen[pair] || nonces[e.Request.InvocationID] || row.Key.Kind != "conformance" {
			t.Fatalf("duplicate or crossed invocation: %+v", row)
		}
		seen[pair], nonces[e.Request.InvocationID] = true, true
		broken := mode == "both" || mode == "replay" && e.Request.Bug.Name == "ReplayCompletedTask" || mode == "reuse" && e.Request.Bug.Name == "ReuseRecoveredName"
		passingStatus := "refused"
		if mode == "alternative" {
			passingStatus = "complete"
		}
		if e.Passed == broken || broken && e.Status != "reproduced" || !broken && e.Status != passingStatus {
			t.Fatalf("wrong replay verdict for %s: %+v", pair, e)
		}
		if e.Request.Path.ModelSHA256 == "" || e.Request.Path.Tool.JarSHA256 == "" || e.Request.Path.Bounds["Token"] != "9007199254740993" {
			t.Fatal("replay lost model/tool/bounds identity")
		}
	}
	if len(r.Application.Conformance) != invocations || len(r.Application.Executions) != 1+3*invocations {
		t.Fatalf("lost execution accounting: %s", pa935JSON(t, r.Application))
	}
	for _, c := range r.Application.Conformance {
		if !c.Result.Passed || c.Result.Runs != 1 || c.Result.Steps != 1 || c.Result.States != 2 || c.Result.Transitions != 1 || c.Result.Exhaustive {
			t.Fatalf("sample missed recovery but replay polluted its counts: %+v", c)
		}
	}
	for _, b := range r.Bugs {
		broken := mode == "both" || mode == "replay" && b.Name == "ReplayCompletedTask" || mode == "reuse" && b.Name == "ReuseRecoveredName"
		if !b.Caught || b.Replay == nil || b.Replay.Counterexample == nil || b.Replay.Passed == broken || b.ReplayStatus().Replayed == broken {
			t.Fatalf("model catch or aggregate replay wrong: %+v", b)
		}
	}
	if explicit {
		if r.Components == nil || len(r.Components.Conformance) != invocations || len(r.Components.Runtime) != 1 {
			t.Fatal("replay erased or duplicated independent component evidence")
		}
		for _, c := range r.Components.Conformance {
			if !c.TransitionsChecked || c.ExhaustiveStates || c.EveryStep {
				t.Fatalf("replay changed independent assurance: %+v", c)
			}
		}
		copy := *r
		before := pa935JSON(t, copy.Application)
		supplied := []ComponentConformanceResult{}
		for _, c := range r.Components.Conformance {
			supplied = append(supplied, c.Result)
		}
		if err := copy.AttributeComponentConformance(&r.Components.Inventory, supplied); err != nil {
			t.Fatal(err)
		}
		if copy.Passed != r.Passed || !bytes.Equal(before, pa935JSON(t, copy.Application)) {
			t.Fatal("supplied evidence rewrote gate replay verdict")
		}
		for _, c := range copy.Components.Conformance {
			if c.Origin != "supplied" {
				t.Fatal("supplied conformance became gate replay")
			}
		}
	} else if r.Components != nil {
		t.Fatal("legacy replay invented component selections")
	}
}

func TestPythonApplicationBugReplayGate935(t *testing.T) {
	pa935API(t, &PythonApplicationSession{})
	tc, _ := pa935Tools(t)
	old := pythonApplicationGateOptions
	t.Cleanup(func() { pythonApplicationGateOptions = old })
	pythonApplicationGateOptions = pa318Options(t, false, new(int))
	for _, explicit := range []bool{false, true} {
		for _, mode := range []string{"fixed", "replay", "reuse", "both"} {
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, mode), func(t *testing.T) {
				_, p := pa935Fixture(t, explicit, mode, "")
				invocations := 1
				if explicit {
					v := p.Manifest.Components.Checks[0].Invocations[1]
					v.ID = "second"
					p.Manifest.Components.Checks[0].Invocations = append(p.Manifest.Components.Checks[0].Invocations, v)
					invocations = 2
					pa310Save(t, p)
				}
				if err := Check(p.Dir); err != nil {
					t.Fatalf("public preflight refused supported replay: %v", err)
				}
				r := pa935Gate(t, p, tc)
				pa935GateAssertions(t, r, explicit, mode, invocations)
			})
		}
	}
	t.Run("sample cannot replace mandatory replay", func(t *testing.T) {
		driver := strings.Replace(pa935Driver, "    if replay(start, steps + bugs):\n        return\n", "", 1)
		_, p := pa935Fixture(t, true, "fixed", driver)
		r := pa935Gate(t, p, tc)
		if r.Passed || !r.Build.Passed || len(r.Application.Conformance) != 1 || !r.Application.Conformance[0].Result.Passed {
			t.Fatalf("missing replay changed independent evidence: %s", pa935JSON(t, r))
		}
		if len(pa935CheckedOf(t, r)) != 2 {
			t.Fatal("missing replay invocation was not accounted for")
		}
		for _, b := range r.Bugs {
			if b.Replay == nil || b.Replay.Passed || b.Replay.Message == "" || b.Replay.Status == "pending" {
				t.Fatalf("missing replay silently passed: %+v", b)
			}
		}
	})
	t.Run("normal valid alternative", func(t *testing.T) {
		_, p := pa935Fixture(t, true, "alternative", "")
		pa935GateAssertions(t, pa935Gate(t, p, tc), true, "alternative", 1)
	})
	t.Run("one invocation cannot cover another", func(t *testing.T) {
		driver := "import sys\n" + strings.Replace(pa935Driver, "    if replay(start, steps + bugs):", "    if 'omit-replay' not in sys.argv and replay(start, steps + bugs):", 1)
		_, p := pa935Fixture(t, true, "fixed", driver)
		v := p.Manifest.Components.Checks[0].Invocations[1]
		v.ID, v.Args = "second", append(append([]string{}, v.Args...), "omit-replay")
		p.Manifest.Components.Checks[0].Invocations = append(p.Manifest.Components.Checks[0].Invocations, v)
		pa310Save(t, p)
		r := pa935Gate(t, p, tc)
		if r.Passed || !r.Build.Passed || len(r.Application.Conformance) != 2 {
			t.Fatal("one missing replay did not fail the gate independently")
		}
		for _, c := range r.Application.Conformance {
			if !c.Result.Passed {
				t.Fatal("unrelated conformance was erased")
			}
		}
		rows := pa935CheckedOf(t, r)
		if len(rows) != 4 {
			t.Fatal("missing bug/invocation pairs")
		}
		for _, row := range rows {
			if row.Evidence.Passed != (row.Key.ID != "second") {
				t.Fatalf("one invocation covered another: %+v", row)
			}
		}
		for _, b := range r.Bugs {
			if b.Replay == nil || b.Replay.Passed || b.ReplayStatus().Replayed {
				t.Fatal("aggregate replay hid failed invocation")
			}
		}
	})
	t.Run("checked refusal is required", func(t *testing.T) {
		t.Setenv("PA935_ALTERNATIVE_FAIL", "1")
		_, p := pa935Fixture(t, false, "fixed", "")
		r := pa935Gate(t, p, tc)
		if r.Passed || !r.Build.Passed || !r.Application.Conformance[0].Result.Passed {
			t.Fatal("TLC replay failure lost independent checks or passed gate")
		}
		for _, b := range r.Bugs {
			if b.Replay == nil || b.Replay.Passed {
				t.Fatal("collector completion substituted for checked replay refusal")
			}
		}
	})
	t.Run("exhaustive mode still replays", func(t *testing.T) {
		driver := strings.Replace(pa935Driver, "    with start() as life:\n        entry, args = random.choice([(app.reload, ()), (app.deliver, (9007199254740993, 'job')), (app.recover, ()), (app.idle, ())])\n        life.step(entry, *args)", "    explore(start, steps)", 1)
		_, p := pa935Fixture(t, true, "fixed", driver)
		p.Manifest.Exhaustive = true
		p.Manifest.Components.Checks[0].Invocations[1].Sampling = map[string]string{"runs": "200", "steps": "8", "seed": "1"}
		pa310Save(t, p)
		if err := Check(p.Dir); err != nil {
			t.Fatal(err)
		}
		r := pa935Gate(t, p, tc)
		if !r.Passed || len(pa935CheckedOf(t, r)) != 2 {
			t.Fatalf("exhaustive gate bypassed or refused replay: %s", pa935JSON(t, r))
		}
		for _, row := range pa935CheckedOf(t, r) {
			if !row.Evidence.Passed || row.Evidence.Observation == nil {
				t.Fatal("exhaustive mode dropped current obligation")
			}
		}
		if len(r.Application.Conformance) != 1 || !r.Application.Conformance[0].Result.Exhaustive {
			t.Fatal("bug replay replaced normal exploration")
		}
	})
	t.Run("model only does not execute replay", func(t *testing.T) {
		_, p := pa935Fixture(t, false, "both", "")
		r, err := RunModel(pa321Context(t), p.Dir, tc)
		if err != nil || r == nil || !r.Passed || !r.ModelOnly || r.Application != nil {
			t.Fatalf("model-only invoked code: %+v %v", r, err)
		}
		for _, b := range r.Bugs {
			if b.Replay == nil || b.Replay.Status != "pending" || b.Replay.Result != nil {
				t.Fatalf("model-only fabricated replay: %+v", b)
			}
		}
	})
}

func TestPythonApplicationBugReplayFingerprints935(t *testing.T) {
	pa935API(t, &PythonApplicationSession{})
	root, p := pa935Fixture(t, true, "fixed", "")
	s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
	q := pa935Request(t, p, false)
	x, y := pa935Run(t, s, q), pa935Run(t, s, q)
	if x.Fingerprint == "" || x.Fingerprint != y.Fingerprint || pa935RawOf(t, x).Request.InvocationID == pa935RawOf(t, y).Request.InvocationID {
		t.Fatal("fresh replay identity changed stable fingerprint or was reused")
	}
	otherRoot, other := pa935Fixture(t, true, "fixed", "")
	z := pa935Run(t, pa315Prepare(t, other, pa318Options(t, false, new(int))), pa935Request(t, other, false))
	if z.Fingerprint != x.Fingerprint {
		t.Fatal("relocation changed stable execution")
	}
	bytesX := pa935JSON(t, x)
	for _, secret := range []string{root, otherRoot, "authentication.key", "hmac_sha256"} {
		if bytes.Contains(bytesX, []byte(secret)) {
			t.Fatalf("portable execution leaked %q", secret)
		}
	}
	var round PythonApplicationExecutionResult
	if err := json.Unmarshal(bytesX, &round); err != nil {
		t.Fatal(err)
	}
	if pythonApplicationExecutionFingerprint(round) != x.Fingerprint || !bytes.Contains(bytesX, []byte("9007199254740993")) {
		t.Fatal("round trip lost exact replay evidence")
	}
	for _, change := range []string{"action", "argument", "result", "refusal", "source", "readback", "request"} {
		t.Run(change, func(t *testing.T) {
			var wire map[string]any
			d := json.NewDecoder(bytes.NewReader(bytesX))
			d.UseNumber()
			if err := d.Decode(&wire); err != nil {
				t.Fatal(err)
			}
			raw := wire["bug_replay"].(map[string]any)
			result := raw["result"].(map[string]any)
			calls := result["calls"].([]any)
			observations := wire["observations"].([]any)
			switch change {
			case "action":
				calls[3].(map[string]any)["action"] = "OtherAction"
			case "argument":
				calls[3].(map[string]any)["args"].([]any)[0].(map[string]any)["atom"] = "9007199254740992"
			case "result":
				result["outcome"] = "complete"
			case "refusal":
				observations[4].(map[string]any)["refusal"].(map[string]any)["attributes"].(map[string]any)["code"] = json.Number("410")
			case "source":
				observations[1].(map[string]any)["source_sha256"] = "changed"
			case "readback":
				observations[1].(map[string]any)["readback"].(map[string]any)["phase"] = "another.phase"
			case "request":
				raw["request"].(map[string]any)["path"].(map[string]any)["model_sha256"] = "changed"
			}
			var changed PythonApplicationExecutionResult
			if err := json.Unmarshal(pa935JSON(t, wire), &changed); err != nil {
				t.Fatal(err)
			}
			if pythonApplicationExecutionFingerprint(changed) == x.Fingerprint {
				t.Fatalf("%s evidence omitted from fingerprint", change)
			}
		})
	}
	var output bytes.Buffer
	if err := writePythonApplicationRecording(pa321Context(t), &output, *x); err == nil {
		t.Fatal("deterministic bug replay became random sampling or exploration coverage")
	}
	tc, _ := pa935Tools(t)
	old := pythonApplicationGateOptions
	t.Cleanup(func() { pythonApplicationGateOptions = old })
	pythonApplicationGateOptions = pa318Options(t, false, new(int))
	r := pa935Gate(t, p, tc)
	before := Fingerprint(*r)
	var decoded Report
	if err := json.Unmarshal(pa935JSON(t, r), &decoded); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(decoded) != before || len(pa935CheckedOf(t, &decoded)) != 2 {
		t.Fatal("report round trip discarded replay checks")
	}
	var wire map[string]any
	d := json.NewDecoder(bytes.NewReader(pa935JSON(t, r)))
	d.UseNumber()
	if err := d.Decode(&wire); err != nil {
		t.Fatal(err)
	}
	app := wire["application"].(map[string]any)
	app["bug_replays"].([]any)[0].(map[string]any)["evidence"].(map[string]any)["passed"] = false
	if err := json.Unmarshal(pa935JSON(t, wire), &decoded); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(decoded) == before {
		t.Fatal("per-invocation replay verdict omitted from report fingerprint")
	}
	pa935GateAssertions(t, r, true, "fixed", 1)
}
