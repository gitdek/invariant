package verify_test

// Issue #327, one plumbing contract, replacing the four previous issues.
//
// Shared capability preflight is used by Check, Run, Python.Check and session
// preparation before verification tools, image acquisition or installations.
// Only existing Python sampled application execution is newly admitted.
// Explicit tests use literal python -m unittest and explicit dotted selections;
// conformance uses the manifest's contained script and selected lexical driver,
// with literal ordered arguments/cwd/env and declared versus effective sampling.
// Keep reserved env checks, 1000/40/1 sampling defaults and existing maxima.
// Missing executable invocations for selected tests/conformance, proof selections
// (even without invocations), unsupported languages/modes, inconsistent existing
// selections, mismatched drivers, exhaustive and attempted-step requests fail.
// Malformed explicit configurations must never enter a legacy fallback.
// Neighbors with no selected checks are legal, visible and unchecked.
//
// Without components ResolveComponents stays legacy and reports have no invented
// component inventory or checks. Bind execution to selected existing sources,
// actual lexical declarations and surrounding application entries using the same
// trusted collector; account for every selected source, including directories.
// Preserve ordinary unittest discovery in this mode, with actual test outcomes.
// Discovery of execution bindings is not inference of production scope.
//
// Stage application, resources, tests, manifest, lock, driver, model and local
// includes before any execution/TLC. Resolve again from staged bytes, retaining
// freshness/containment checks. Every consumer, including attribution and hashes,
// uses the snapshot. Select the admitted application-root dependency image once
// with immutable identity and original/canonical/resolution/artifact evidence;
// verification-local requirements cannot replace it. Keep offline linux/amd64,
// startup/import policy, hash seed 0, read-only inputs, limits and cleanup.
// No annotations are required; even a +nagini marker cannot enable proof here.
// Preserve D-0183: stateless handles and private read-back/continuity records,
// authenticated complete streams and exact target/driver/entry binding. Access
// to collector internals is gate tampering, not a claim of isolation from all
// hostile in-process Python. Do not weaken this boundary or reopen #323.
//
// Translate authenticated runs to existing conformance.Check, preserving run
// boundaries, ordering, exact scalars, stuttering/refusal semantics and batching.
// TLC checks Init and Next at ratified bounds; no successful process or driver
// trace alone qualifies. Required tests must actually run and pass; skipped,
// not_run, expected_failure, unexpected_success, fixture/import/runtime failure
// and malformed/missing evidence fail, with check-specific diagnostics. Preserve
// independent valid observations when another check fails, not unauthenticated
// partial conformance. Continue safe independent checks; keep cancellation and
// operational errors distinguishable. Existing design/pin/ratification/witness/
// fairness/property/known-bug checks still apply. Reject non-sampled output.
//
// Report JSON adds optional "application" for actual application execution,
// both explicit and legacy. Its "executions" are PythonApplicationExecutionResult
// values, its "conformance" records associate a "key" and TLC "result" with
// the exact executed invocation; include model/includes, bounds, actual tool pins
// and diagnostics there or in its provenance. Retain immutable execution image
// identity for audit, excluding local image IDs from portable fingerprints.
// No row may be manufactured from an intended invocation. Each component gets
// only its own successful conformance via AttributeComponentConformance, runtime
// exercise via AttributeComponentRuntime, assumptions assumed/unverified, and
// no proof or target runtime coverage. No invented assertions. Preserve complete
// inventories including files with no functions. Keep invocation matching exact;
// record actual launcher separately from the selected declared command.
//
// Conformance/runtime component evidence rows add origin "gate" or "supplied".
// The public attribution APIs ALWAYS classify incoming records as supplied,
// retain other kinds and their origins, and revalidate incoming/retained evidence
// transactionally, including JSON round trips. Clearing one kind preserves the
// others; invalid/stale updates are atomic; supplied rows never change Passed.
// Gate origin is descriptive provenance, not an authorization token or verdict.
// Legacy externally supplied rows without origins retain supplied semantics.
//
// JSON/Markdown/fingerprints describe what ran and TLC checked, with complete
// application files/resources, requirements/canonical inputs/wheel resolution,
// driver/runner/observer, model/includes, ratified bounds, effective sampling,
// observations/counts/outcomes/diagnostics. Changed inputs OR observations change
// fingerprints even with equal counts/outcomes. Exclude time, host paths and
// transient container/authentication IDs; retain portable content identity.
// Safely render all untrusted fields in both explicit and legacy application
// receipts using existing literal rules. Old legacy receipts stay unchanged.
// Only sampled tested-against-the-model evidence is granted: no exhaustive-code,
// every-step, proof or project-wide every-size claim. Distinguish exhaustive
// design checking from sampled implementation testing.
//
// Update only obsolete public-existing-Python refusal assertions in the named
// old tests; retain all genuine unsupported, discovery, session, collector,
// attribution, old Python/marked-file Python, TypeScript and model-only tests.
// Add builder-owned regressions for snapshot mutation/cancellation/cleanup,
// selected files/directories/multiple targets, preserved independent outcomes,
// missing images, unsupported bindings, safe literal rendering of every new
// field, dependency/runner/observer fingerprint sensitivity and old receipts.
//
// SPEC and README replace ALL obsolete blanket unavailability claims. README
// supplies a complete usable manual-adoption example (application/verification
// layout, existing and component selections, omitted-components variant,
// unittest/driver invocations, dependency/resource, full model and bounds,
// read-back driver, actual human pin/ratification workflow, verify command,
// receipt/failure expectations). No invented ratifier, ratification or decision,
// no edits to existing pinned statements/locks, no factory drafting/synthesis
// or proof adapters. Exhaustive/attempt and larger-bound integration is the
// next parent issue, not an unplanned follow-up within #327.
// Cite recorded foundations on changed SPEC bullets: D-0013,D-0024,D-0029,
// D-0047,D-0054,D-0158,D-0161,D-0163,D-0168,D-0169,D-0170,D-0171,D-0175,
// D-0177,D-0178,D-0180,D-0181,D-0183 and D-0122 as applicable. This ratified
// plan supplies new gate/receipt policy pending merger recording with decision
// tooling. The builder cannot write decisions/. Decision recording/unavailable
// Docker/network checks belong under Merger work and alone require no repair.
//
// Default tests execute REAL local Python via the production embedded runner;
// only Docker transport/TLC results are doubles. They do not claim real TLC or
// Docker validation. No acceptance function/subtest skips. With
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 the same suite additionally runs
// verify.Run with real Docker, Python, TLC and installed packaging wheel. Missing
// prerequisites FAIL. Merger must post passing final-head output for:
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify \
//   ./internal/conformance ./cmd/invariant -run '^TestPythonApplicationGate' \
//   -count=1 -timeout 45m
// Extend real builder-owned regressions for ALL requirements above, including
// actual dependency resolution sensitivity, snapshot/cancellation/no-Nagini,
// independent multiple checks, runtime non-sampled output and original bytes.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const pa327Project = "verification/lifecycle"
const pa327Source = "tasks/lifecycle.py"
const pa327DriverPath = pa327Project + "/driver.py"
const pa327ModelPath = pa327Project + "/.invariant/specs/Lifecycle.tla"
const pa327Model = `---- MODULE Lifecycle ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLE task
vars == <<task>>
Init == task = "pending"
Next == \/ /\ task = "pending" /\ task' = "running"
        \/ /\ task = "running" /\ task' = "done"
        \/ /\ task = "done" /\ UNCHANGED task
Spec == Init /\ [][Next]_vars
Safe == task \in {"pending", "running", "done"} /\ Limit >= 1 /\ Helper
Seen == task = "done"
Bad == task' = "broken"
Fair == WF_vars(Next)
Eventually == <> (task = "done")
====
`
const pa327Driver = `from invariant_observe import functional
from tasks.lifecycle import advance
from tasks import application
def drive():
    with functional(advance, application.start, argument='state', field='task') as life:
        life.step(application.tick)
        life.step(application.tick)
        life.step(application.tick)
if __name__ == '__main__':
    drive()
`
const pa327Test = `import unittest
from tasks import application
class TaskTests(unittest.TestCase):
    def test_pass(self):
        self.assertEqual(application.tick('pending'), 'running')
    def test_other(self):
        self.assertEqual(application.tick('running'), 'done')
`

type pa327Fixture struct {
	root string
	p    *project.Project
}

func pa327Write(t *testing.T, root, name, body string) {
	t.Helper()
	f := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
func pa327JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func pa327Save(t *testing.T, f pa327Fixture) {
	t.Helper()
	pa327Write(t, f.p.Dir, ".invariant/invariant.json", string(pa327JSON(t, f.p.Manifest)))
	pa327Write(t, f.p.Dir, ".invariant/ratified.lock", string(pa327JSON(t, f.p.Lock)))
}
func pa327Ref(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}
func pa327ID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}
func pa327New(t *testing.T, explicit, wheel bool) pa327Fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	p := &project.Project{Dir: filepath.Join(root, pa327Project), Manifest: project.Manifest{Name: "sampled task lifecycle", Language: "python", Code: ".", Module: ".invariant/specs/Lifecycle.tla", Conformance: "driver.py", Existing: []string{pa327Source}}, Lock: project.Lock{Decision: "test-fixture-only", Bounds: map[string]string{"Limit": "2"}}}
	for _, s := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}, {Name: "Seen", Kind: project.Witness}, {Name: "Bad", Kind: project.Bug, Expect: "Safe"}, {Name: "Fair", Kind: project.Fairness}, {Name: "Eventually", Kind: project.Property}} {
		h, e := tla.PinHash(pa327Model, s.Name, project.Model)
		if e != nil {
			t.Fatal(e)
		}
		s.SHA256 = h
		s.Says = s.Name + " fixture obligation"
		p.Lock.Statements = append(p.Lock.Statements, s)
	}
	files := map[string]string{
		"tasks/__init__.py": "", pa327Source: "from .helper import next_state\ndef advance(state):\n    return next_state(state)\n",
		"tasks/helper.py": "from pathlib import Path\ndef next_state(state):\n    if state == 'pending':\n        return Path(__file__).with_name('next.txt').read_text().strip()\n    return 'done'\n",
		"tasks/next.txt":  "running\n", "tasks/application.py": "from .lifecycle import advance\ndef start():\n    return 'pending'\ndef tick(state):\n    return advance(state)\n",
		"tasks/neighbor.py": "def unchecked():\n    return 17\n", "tasks/constants.py": "VALUE = 19\n", "resources/payload.txt": "unchanged resource\n",
		"tests/__init__.py": "", "tests/test_tasks.py": pa327Test, pa327DriverPath: pa327Driver, pa327ModelPath: pa327Model,
		pa327Project + "/.invariant/specs/Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n",
		"assumptions.txt": "The local clock is monotonic.\n",
	}
	if wheel {
		files["requirements.txt"] = "-r deps/application.txt\n"
		files["deps/application.txt"] = "packaging>=24,<25\n-c constraints.txt\n"
		files["deps/constraints.txt"] = "packaging==24.2\n"
		files[pa327Project+"/requirements.txt"] = "must-not-be-installed==0\n"
		files["tasks/helper.py"] = "from packaging.version import Version\nassert Version('1.9') < Version('1.10')\n" + files["tasks/helper.py"]
	}
	if explicit {
		unit := pa327Ref("tests/test_tasks.py", "TaskTests.test_pass")
		driver := pa327Ref(pa327DriverPath, "drive")
		p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"tasks"}, Checks: []project.ComponentCheck{{Target: pa327Ref(pa327Source, "advance"), Tests: []project.ComponentRef{unit}, Conformance: []project.ComponentRef{driver}, Assumptions: []project.ComponentAssumption{{ID: "clock", Statement: "Clock is monotonic", Reason: "Environment premise", Source: "assumptions.txt"}}, Invocations: []project.ComponentInvocation{{ID: "unit", Kind: "test", Check: unit, Command: "python", Args: []string{"-m", "unittest", "tests.test_tasks.TaskTests.test_pass"}, WorkingDir: ".", Inputs: []string{"tasks/next.txt"}}, {ID: "sample", Kind: "conformance", Check: driver, Command: "python", Args: []string{"driver.py", "literal argument"}, WorkingDir: pa327Project, Sampling: map[string]string{"runs": "2", "steps": "4", "seed": "1"}, Inputs: []string{pa327ModelPath, pa327Project + "/.invariant/specs/Local.tla", pa327Project + "/.invariant/ratified.lock"}}}}}}
	}
	for name, body := range files {
		pa327Write(t, root, name, body)
	}
	f := pa327Fixture{root, p}
	pa327Save(t, f)
	return f
}
func pa327Tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(name)
		if e != nil {
			return e
		}
		r, e := filepath.Rel(root, name)
		if e != nil {
			return e
		}
		h := sha256.Sum256(b)
		out[filepath.ToSlash(r)] = "sha256:" + hex.EncodeToString(h[:])
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func pa327Admit(t *testing.T, f pa327Fixture) {
	t.Helper()
	if e := verify.Check(f.p.Dir); e != nil {
		t.Fatalf("sampled existing Python must be admitted: %v", e)
	}
}

// This double substitutes Docker transport and emits labelled fixture TLC
// responses, but executes the actual staged runner and collector with Python.
// It never constructs a passing Python event stream or bypasses authentication.
const pa327Docker = `import os,sys,json,subprocess,pathlib
args=sys.argv[1:]
log=os.environ['PA327_LOG']
def record(row):
    with open(log,'a') as f: f.write(json.dumps(row,sort_keys=True)+'\n')
def msg(code,text):
    print('@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@' % (code,text,code))
if args and args[0]=='rm': sys.exit(0)
if args[:2]==['image','inspect']:
    record({'kind':'image','args':args}); print('linux/amd64'); sys.exit(0)
if args and args[0]=='info':
    record({'kind':'info'}); print('pa327 deliberately unavailable Docker'); sys.exit(73)
mounts={}
for i,a in enumerate(args[:-1]):
    if a=='-v':
        host,guest,*rest=args[i+1].split(':'); mounts[guest]=host
def mapped(s):
    for guest,host in sorted(mounts.items(),key=lambda p:-len(p[0])):
        if s==guest or s.startswith(guest+'/'): return host+s[len(guest):]
    return s
if 'tlc2.TLC' in args:
    work=mounts['/work']; module=args[-1].removesuffix('.tla')
    body=pathlib.Path(work,module+'.tla').read_text(); cfg=pathlib.Path(work,'Invariant.cfg').read_text()
    model=pathlib.Path(work,'Lifecycle.tla').read_text(); include=pathlib.Path(work,'Local.tla').read_text()
    record({'kind':'tlc','module':module,'body':body,'cfg':cfg,'model':model,'include':include})
    msg(2262,'TLC2 Version 2.19 of 08 August 2024')
    failure=os.environ.get('PA327_FAIL','')
    if module.startswith('Witness_'):
        if failure!='witness': msg(2110,'Invariant Invariant_Never_Seen is violated.');msg(2217,'1: <Initial predicate>\n/\\ task = "done"');sys.exit(12)
    elif module.startswith('Bug_'):
        if failure!='bug': msg(2110,'Invariant Safe is violated.');msg(2217,'1: <Initial predicate>\n/\\ task = "broken"');sys.exit(12)
    elif module.startswith('Fairness_') and failure=='fairness': msg(2116,'Temporal properties were violated.');sys.exit(13)
    elif module.startswith('Property_') and failure=='property': msg(2116,'Temporal properties were violated.');sys.exit(13)
    elif module=='Lifecycle' and failure=='design': msg(2110,'Invariant Safe is violated.');sys.exit(12)
    elif module=='Invariant_ConformanceInit' and ('"alien"' in body or failure=='init'):
        msg(2110,'Invariant Init is violated.');msg(2217,'1: <Initial predicate>\n/\\ task = "alien"');sys.exit(12)
    elif module=='Invariant_Conformance' and ('"broken"' in body or failure=='step'):
        msg(2114,'Deadlock reached.');msg(2217,'1: <Initial predicate>\n/\\ task = "pending"\n/\\ invariant_source = 1\n/\\ invariant_target = 2');sys.exit(11)
    elif failure=='transport' and module.startswith('Invariant_Conformance'):
        print('forced TLC transport error');sys.exit(99)
    msg(2193,'Model checking completed. No error has been found.');msg(2199,'4 states generated, 3 distinct states found, 0 states left on queue.');msg(2194,'The depth of the complete state graph search is 3.');sys.exit(0)
if '/usr/local/bin/python' in args:
    pos=args.index('/usr/local/bin/python'); argv=args[pos+1:]
    if '/runtime/runner.py' not in argv: record({'kind':'forbidden','args':args});sys.exit(94)
    env={}; start=args.index('-i')+1
    for item in args[start:pos]:
        k,v=item.split('=',1);env[k]=v
    request=pathlib.Path(mapped('/runtime/request.json')); req=json.loads(request.read_text())
    def remap(v):
        if isinstance(v,str):return mapped(v)
        if isinstance(v,dict):return {k:remap(x) for k,x in v.items()}
        if isinstance(v,list):return [remap(x) for x in v]
        return v
    local=pathlib.Path(mounts['/output'],'local-request.json');local.write_text(json.dumps(remap(req)))
    argv=[str(local) if a=='/runtime/request.json' else mapped(a) for a in argv]
    # Host 3.9/3.10 lack -P; production Docker keeps the exact pinned flags.
    if sys.version_info < (3,11): argv=[a for a in argv if a!='-P']
    wd=mapped(args[args.index('-w')+1])
    record({'kind':'python','key':req.get('key'),'argv':args[pos+1:],'env':env,'image':next((a for a in args if a.startswith('python@') or a.startswith('sha256:')),''),'snapshot':req.get('snapshot_sha256')})
    result=subprocess.run([os.environ['PA327_PYTHON']]+argv,cwd=wd,env=env)
    corrupt=os.environ.get('PA327_EVENTS','')
    events=pathlib.Path(mounts['/output'],'events.jsonl')
    if req.get('key',{}).get('kind')=='conformance' and corrupt:
        if corrupt=='missing': events.unlink(missing_ok=True)
        else: events.write_text('{malformed}\n')
    sys.exit(result.returncode)
record({'kind':'forbidden','args':args}); print('unexpected Docker operation',args);sys.exit(93)
`

func pa327Fake(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	py, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	pa327Write(t, bin, "docker.py", pa327Docker)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	launcher := "#!/bin/sh\nexec " + quote(py) + " " + quote(filepath.Join(bin, "docker.py")) + " \"$@\"\n"
	pa327Write(t, bin, "docker", launcher)
	if e := os.Chmod(filepath.Join(bin, "docker"), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PA327_LOG", log)
	t.Setenv("PA327_PYTHON", py)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PA327_FAIL", "")
	t.Setenv("PA327_EVENTS", "")
	t.Cleanup(func() {
		b, _ := os.ReadFile(log)
		if strings.Contains(string(b), `"kind": "forbidden"`) {
			t.Errorf("unexpected tool/proof preparation or execution: %s", b)
		}
	})
	return toolchain.Toolchain{JavaImage: toolchain.JavaImage, TLCJar: filepath.Join(t.TempDir(), "fixture.jar"), PythonImage: toolchain.PythonImage}, log
}
func pa327Run(t *testing.T, f pa327Fixture, tc toolchain.Toolchain) *verify.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r, e := verify.Run(ctx, f.p.Dir, "", tc)
	if e != nil || r == nil {
		t.Fatalf("gate must return an actual-result receipt: report=%+v err=%v", r, e)
	}
	return r
}

type pa327App struct {
	Executions  []verify.PythonApplicationExecutionResult `json:"executions"`
	Conformance []struct {
		Key    verify.PythonApplicationInvocationKey `json:"key"`
		Result json.RawMessage                       `json:"result"`
	} `json:"conformance"`
}

func pa327Application(t *testing.T, r *verify.Report) pa327App {
	t.Helper()
	var top map[string]json.RawMessage
	if e := json.Unmarshal(pa327JSON(t, r), &top); e != nil {
		t.Fatal(e)
	}
	var a pa327App
	if len(top["application"]) == 0 {
		t.Fatal("receipt omits actual application execution")
	}
	if e := json.Unmarshal(top["application"], &a); e != nil {
		t.Fatal(e)
	}
	if len(a.Executions) == 0 {
		t.Fatal("no executed invocation evidence")
	}
	return a
}
func pa327Assert(t *testing.T, f pa327Fixture, r *verify.Report, pass bool) {
	t.Helper()
	if r.Passed != pass {
		t.Fatalf("passed=%v want %v: %s", r.Passed, pass, pa327JSON(t, r))
	}
	a := pa327Application(t, r)
	if r.Code != nil || r.Toolchain.NaginiRecipe != "" || r.Larger != nil {
		t.Fatalf("sampled application gained proof/larger evidence: %+v", r)
	}
	if r.Conformance != nil && (r.Conformance.Exhaustive || r.Conformance.Tried != nil) {
		t.Fatal("sampled evidence acquired exhaustive or attempted-step claim")
	}
	if pass {
		if len(a.Conformance) == 0 {
			t.Fatal("successful gate lacks TLC result")
		}
		seen := false
		for _, x := range a.Executions {
			if !x.Success || !x.Complete {
				t.Fatalf("success with incomplete execution: %+v", x)
			}
			if x.Actual.Env["PYTHONHASHSEED"] != "0" || x.Provenance.RunnerSHA256 == "" || x.Provenance.ObserverSHA256 == "" || len(x.Provenance.Files) == 0 {
				t.Fatalf("missing actual provenance: %+v", x)
			}
			if x.Key.Kind == "conformance" {
				seen = true
				if x.ObservationMode != "sampled" || len(x.Observations) < 2 {
					t.Fatal("success without bound sampled observations")
				}
			}
		}
		if !seen {
			t.Fatal("no driver execution")
		}
	}
	if f.p.Manifest.Components == nil {
		if r.Components != nil {
			t.Fatal("omitted components gained inventory")
		}
		if pass && r.CodeAssurance() != "tested against the model" {
			t.Fatal(r.CodeAssurance())
		}
		return
	}
	if r.Components == nil || len(r.Components.Inventory.Components) < 3 {
		t.Fatal("explicit inventory/neighbors missing")
	}
	for _, p := range r.Components.Proofs {
		if p.Status != "unchecked" {
			t.Fatalf("invented proof: %+v", p)
		}
	}
	for _, c := range r.Components.Conformance {
		if c.ExhaustiveStates || c.EveryStep {
			t.Fatal("invented exhaustive conformance")
		}
		if c.Target != pa327ID(pa327Source, "advance") {
			t.Fatalf("neighbor gained conformance: %+v", c)
		}
	}
	for _, row := range r.Components.Runtime {
		for _, test := range row.Result.Tests {
			if len(test.Assertions) > 0 {
				t.Fatal("invented assertion evidence")
			}
		}
	}
	for _, a := range r.Components.Assumptions {
		if a.Verified || a.Status != "assumed" {
			t.Fatalf("assumption verified: %+v", a)
		}
	}
	if pass {
		if len(r.Components.Conformance) != 1 || !r.Components.Conformance[0].TransitionsChecked || len(r.Components.Runtime) != 1 {
			t.Fatalf("actual independent component attribution missing: %+v", r.Components)
		}
		v := r.Components.Conformance[0].Result
		if v.Invocation == nil || v.Invocation.ID != "sample" || v.Check != pa327ID(pa327DriverPath, "drive") || v.Runner == nil || v.Model == nil || v.Bounds == nil || len(v.Observations) == 0 {
			t.Fatalf("attribution incomplete: %+v", v)
		}
	}
}

func TestPythonApplicationGateAdmission(t *testing.T) {
	_, log := pa327Fake(t)
	for _, explicit := range []bool{true, false} {
		for _, marked := range []bool{false, true} {
			// Exhaustive applications are admitted since #342.
			for _, exhaustive := range []bool{false, true} {
				f := pa327New(t, explicit, false)
				if marked {
					b, _ := os.ReadFile(filepath.Join(f.root, pa327Source))
					pa327Write(t, f.root, pa327Source, "# +nagini\n"+string(b))
				}
				f.p.Manifest.Exhaustive = exhaustive
				pa327Save(t, f)
				pa327Admit(t, f)
			}
		}
	}
	cases := []struct {
		name   string
		change func(pa327Fixture)
	}{
		{"proof selection without invocation", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Proof = &project.ComponentProof{Contracts: []project.ComponentRef{pa327Ref(pa327Source, "advance")}}
		}},
		{"missing selected test invocation", func(f pa327Fixture) { c := &f.p.Manifest.Components.Checks[0]; c.Invocations = c.Invocations[1:] }},
		{"missing selected conformance invocation", func(f pa327Fixture) { c := &f.p.Manifest.Components.Checks[0]; c.Invocations = c.Invocations[:1] }},
		{"no usable conformance", func(f pa327Fixture) {
			c := &f.p.Manifest.Components.Checks[0]
			c.Conformance = nil
			c.Invocations = c.Invocations[:1]
		}},
		{"unsupported command", func(f pa327Fixture) { f.p.Manifest.Components.Checks[0].Invocations[0].Command = "sh" }},
		{"implicit unittest", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest"}
		}},
		{"explicit discovery", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "discover"}
		}},
		{"unknown sampling mode", func(f pa327Fixture) { f.p.Manifest.Components.Checks[0].Invocations[1].Sampling["mode"] = "attempted" }},
		{"unsupported sampling", func(f pa327Fixture) { f.p.Manifest.Components.Checks[0].Invocations[1].Sampling["steps"] = "0" }},
		{"reserved environment", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Invocations[1].Env = map[string]string{"PYTHONPATH": "."}
		}},
		{"different manifest driver", func(f pa327Fixture) {
			pa327Write(t, f.p.Dir, "other.py", pa327Driver)
			f.p.Manifest.Conformance = "other.py"
		}},
		{"target outside existing selection", func(f pa327Fixture) { f.p.Manifest.Existing = []string{"tasks/neighbor.py"} }},
		{"bad explicit scope", func(f pa327Fixture) { f.p.Manifest.Components.Scope = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pa327New(t, true, false)
			tc.change(f)
			pa327Save(t, f)
			if e := verify.Check(f.p.Dir); e == nil {
				t.Fatal("unsupported configuration admitted")
			}
			if r, e := verify.Run(context.Background(), f.p.Dir, "", toolchain.Toolchain{}); e == nil || r != nil {
				t.Fatalf("invalid preflight returned report: %+v %v", r, e)
			}
			if b, _, e := (verify.Python{}).Check(context.Background(), f.p); e == nil || b.Passed {
				t.Fatal("direct Python bypasses capability preflight")
			}
			n := 0
			s, e := verify.PreparePythonApplicationSession(context.Background(), f.p, filepath.Join(t.TempDir(), "stage"), verify.PythonApplicationSessionOptions{SelectImage: func(context.Context, *verify.PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				n++
				return toolchain.PythonImage, nil, nil
			}})
			if e == nil || s != nil || n != 0 {
				if s != nil {
					s.Close()
				}
				t.Fatalf("session accepted invalid configuration or selected image: %v calls=%d", e, n)
			}
		})
	}
	if b, _ := os.ReadFile(log); len(b) > 0 {
		t.Fatalf("preflight invoked tools: %s", b)
	}
}

func TestPythonApplicationGateSessions(t *testing.T) {
	pa327Fake(t)
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			f := pa327New(t, explicit, false)
			pa327Admit(t, f)
			n := 0
			stage := filepath.Join(t.TempDir(), "stage")
			s, e := verify.PreparePythonApplicationSession(context.Background(), f.p, stage, verify.PythonApplicationSessionOptions{SelectImage: func(_ context.Context, w *verify.PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				n++
				if w.Root != stage || w.Project.Dir == f.p.Dir {
					t.Fatal("image selection did not receive staged context")
				}
				if explicit != (w.Inventory.Mode == "explicit") {
					t.Fatal("inventory mode changed")
				}
				return toolchain.PythonImage, nil, nil
			}})
			if e != nil || s == nil {
				t.Fatalf("prepare explicit=%v: %v", explicit, e)
			}
			defer s.Close()
			before := s.Provenance()
			for _, name := range []string{pa327Source, pa327DriverPath, pa327ModelPath, "tasks/next.txt", "tests/test_tasks.py", pa327Project + "/.invariant/specs/Local.tla"} {
				pa327Write(t, f.root, name, "original changed after preparation\n")
			}
			for repeat := 0; repeat < 2; repeat++ {
				keys := s.Invocations()
				if len(keys) < 2 {
					t.Fatal("session omitted tests or driver")
				}
				seen := false
				for _, v := range keys {
					r, e := s.Run(context.Background(), v.Key)
					if e != nil || r == nil || !r.Success {
						t.Fatalf("snapshot execution: %+v %v", r, e)
					}
					seen = seen || len(r.Observations) > 0
					if r.Provenance.Fingerprint != before.Fingerprint {
						t.Fatal("session reread original provenance")
					}
				}
				if !seen {
					t.Fatal("session never collected bound observations")
				}
			}
			if n != 1 {
				t.Fatalf("image selected %d times", n)
			}
			if !reflect.DeepEqual(before, s.Provenance()) {
				t.Fatal("session provenance mutated")
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if r, e := s.Run(context.Background(), s.Invocations()[0].Key); e == nil || (r != nil && r.Success) {
				t.Fatal("closed session executed")
			}
		})
	}
}

func TestPythonApplicationGateResults(t *testing.T) {
	t.Run("local Python and recording TLC", func(t *testing.T) {
		tc, log := pa327Fake(t)
		pa327Results(t, tc, false)
		b, e := os.ReadFile(log)
		if e != nil {
			t.Fatal(e)
		}
		for _, want := range []string{`"module": "Invariant_ConformanceInit"`, `"module": "Invariant_Conformance"`, `CONSTANT Limit = 2`, `"kind": "python"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("gate did not perform %s", want)
			}
		}
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python TLC and wheel", func(t *testing.T) {
			tc, e := toolchain.Ensure(context.Background())
			if e != nil {
				t.Fatalf("requested real prerequisites: %v", e)
			}
			pa327Results(t, tc, true)
		})
	}
}
func pa327Results(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	for _, explicit := range []bool{true, false} {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%v/marked=%v", explicit, marked), func(t *testing.T) {
				f := pa327New(t, explicit, wheel)
				if marked {
					b, _ := os.ReadFile(filepath.Join(f.root, pa327Source))
					pa327Write(t, f.root, pa327Source, "# +nagini\n"+string(b))
				}
				before := pa327Tree(t, f.root)
				pa327Admit(t, f)
				r := pa327Run(t, f, tc)
				pa327Assert(t, f, r, true)
				a := pa327Application(t, r)
				for _, x := range a.Executions {
					if x.Key.Kind == "conformance" {
						if len(x.Observations) != 4 {
							t.Fatalf("actual observations lost: %+v", x.Observations)
						}
						if x.Observations[0].State["task"] != "pending" || x.Observations[1].After["task"] != "running" || x.Observations[2].After["task"] != "done" {
							t.Fatal("read-back changed")
						}
					}
					if wheel {
						d := x.Provenance.Dependencies
						if d == nil || len(d.Artifacts) != 1 || d.Artifacts[0].Name != "packaging" || d.Artifacts[0].Version != "24.2" || len(d.Inputs) != 3 || len(d.CanonicalInputs) != 2 {
							t.Fatalf("real wheel evidence missing: %+v", d)
						}
					}
				}
				md := receipt.Markdown(r)
				for _, want := range []string{"sampled", "pending", "running", "done", "driver.py"} {
					if !strings.Contains(md, want) {
						t.Errorf("receipt omits %q", want)
					}
				}
				if strings.Contains(md, "**proved**") || strings.Contains(md, "code is **tested in every state") {
					t.Fatal("receipt overclaims sampled evidence")
				}
				var decoded verify.Report
				if e := json.Unmarshal(pa327JSON(t, r), &decoded); e != nil {
					t.Fatal(e)
				}
				if verify.Fingerprint(decoded) != r.Fingerprint {
					t.Fatal("JSON round trip lost evidence fingerprint")
				}
				b, ev, e := (verify.Python{Image: toolchain.PythonImage}).Check(context.Background(), f.p)
				if e != nil || !b.Passed || len(ev.Traces) == 0 {
					t.Fatalf("direct Python application route: %+v %+v %v", b, ev, e)
				}
				if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
					t.Fatal("verification modified original application")
				}
			})
		}
	}
}

func TestPythonApplicationGateFailures(t *testing.T) {
	t.Run("local Python", func(t *testing.T) {
		tc, _ := pa327Fake(t)
		pa327FailureCases(t, tc, false)
		for _, bad := range []string{"missing", "malformed"} {
			t.Run(bad, func(t *testing.T) {
				f := pa327New(t, true, false)
				pa327Admit(t, f)
				t.Setenv("PA327_EVENTS", bad)
				r := pa327Run(t, f, tc)
				pa327Assert(t, f, r, false)
				if len(r.Components.Runtime) == 0 {
					t.Fatal("invalid conformance erased valid runtime test")
				}
			})
		}
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python TLC", func(t *testing.T) {
			tc, e := toolchain.Ensure(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			pa327FailureCases(t, tc, true)
		})
	}
}
func pa327FailureCases(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	cases := []struct {
		name    string
		change  func(*testing.T, pa327Fixture)
		runtime bool
	}{
		{"invalid transition", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tasks/helper.py", "def next_state(state):\n    return 'broken' if state == 'running' else 'running'\n")
		}, false},
		{"invalid initial", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tasks/application.py", "from .lifecycle import advance\ndef start(): return 'alien'\ndef tick(state): return advance(state)\n")
		}, false},
		{"unused target", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, "def drive():\n    print('{\"traces\":[[{\"task\":\"pending\"},{\"task\":\"running\"}]]}')\nif __name__=='__main__': drive()\n")
		}, false},
		{"import only", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, "from tasks.lifecycle import advance\ndef drive():\n    pass\nif __name__=='__main__': drive()\n")
		}, false},
		{"call plus substitute lifecycle", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, "from tasks.lifecycle import advance\ndef drive():\n    advance('pending')\n    print('{\"traces\":[[{\"task\":\"pending\"},{\"task\":\"done\"}]]}')\nif __name__=='__main__': drive()\n")
		}, false},
		{"entry substitutes return", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tasks/application.py", "from .lifecycle import advance\ndef start(): return 'pending'\ndef tick(state):\n    advance(state)\n    return 'done'\n")
		}, false},
		{"handle tampering", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, strings.Replace(pa327Driver, "        life.step(application.tick)", "        try:\n            life.state = 'running'\n        except Exception:\n            pass\n        life.step(application.tick)", 1))
		}, false},
		{"driver import failure", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, "import missing_application_dependency\n"+pa327Driver)
		}, false},
		{"driver runtime failure", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, strings.Replace(pa327Driver, "    with functional", "    raise RuntimeError('driver failed')\n    with functional", 1))
		}, false},
		{"driver not invoked", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, strings.Split(pa327Driver, "if __name__")[0])
		}, false},
		{"malformed output", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, pa327DriverPath, "def drive(): print('{malformed}')\nif __name__=='__main__': drive()\n")
		}, false},
		{"test fails beside passing test", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tests/test_tasks.py", strings.Replace(pa327Test, "self.assertEqual(application.tick('pending'), 'running')", "self.fail('selected test failed')", 1))
			if f.p.Manifest.Components != nil {
				f.p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "tests.test_tasks"}
			}
		}, true},
		{"test skipped", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tests/test_tasks.py", strings.Replace(pa327Test, "    def test_pass", "    @unittest.skip('not exercised')\n    def test_pass", 1))
		}, true},
		{"test expected failure", func(t *testing.T, f pa327Fixture) {
			s := strings.Replace(pa327Test, "    def test_pass", "    @unittest.expectedFailure\n    def test_pass", 1)
			s = strings.Replace(s, "self.assertEqual(application.tick('pending'), 'running')", "self.fail('expected')", 1)
			pa327Write(t, f.root, "tests/test_tasks.py", s)
		}, true},
		{"test unexpected success", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tests/test_tasks.py", strings.Replace(pa327Test, "    def test_pass", "    @unittest.expectedFailure\n    def test_pass", 1))
		}, true},
		{"selected test never run", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tests/test_tasks.py", pa327Test+"\ndef load_tests(loader, tests, pattern):\n    return unittest.TestSuite([TaskTests('test_other')])\n")
			if f.p.Manifest.Components != nil {
				f.p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "tests.test_tasks"}
			}
		}, true},
		{"test fixture error", func(t *testing.T, f pa327Fixture) {
			pa327Write(t, f.root, "tests/test_tasks.py", pa327Test+"\ndef setUpModule():\n    raise RuntimeError('fixture failed')\n")
		}, true},
	}
	for _, explicit := range []bool{true, false} {
		for _, c := range cases {
			if !explicit && c.name == "selected test never run" {
				continue
			} // No explicit required selection exists in discovery mode.
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, c.name), func(t *testing.T) {
				f := pa327New(t, explicit, wheel)
				c.change(t, f)
				pa327Save(t, f)
				pa327Admit(t, f)
				before := pa327Tree(t, f.root)
				r := pa327Run(t, f, tc)
				pa327Assert(t, f, r, false)
				a := pa327Application(t, r)
				if c.runtime {
					found := false
					for _, x := range a.Executions {
						found = found || (x.Key.Kind == "conformance" && x.Success)
					}
					if !found {
						t.Fatal("failed test erased independent conformance execution")
					}
					if explicit && (len(r.Components.Conformance) != 1 || !r.Components.Conformance[0].TransitionsChecked) {
						t.Fatal("failed runtime erased valid checked conformance")
					}
				}
				if c.name == "test fails beside passing test" {
					passing, failed := false, false
					for _, x := range a.Executions {
						for _, row := range x.Tests {
							passing = passing || row.Outcome == "passed"
							failed = failed || row.Outcome == "failed"
						}
					}
					if !passing || !failed {
						t.Fatal("independent test outcomes lost")
					}
				}
				if c.name == "invalid transition" {
					if explicit && (len(r.Components.Conformance) != 1 || r.Components.Conformance[0].TransitionsChecked) {
						t.Fatal("TLC violation credited as success")
					}
				}
				js := string(pa327JSON(t, r))
				if !strings.Contains(js, "diagnostic") && !strings.Contains(js, "message") {
					t.Fatal("failure lacks diagnostic")
				}
				if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
					t.Fatal("failure changed original bytes")
				}
			})
		}
	}
}

func TestPythonApplicationGateDesignObligations(t *testing.T) {
	tc, _ := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Admit(t, f)
	pa327Assert(t, f, pa327Run(t, f, tc), true)
	for _, failure := range []string{"design", "witness", "bug", "fairness", "property", "pin", "ratification", "transport"} {
		t.Run(failure, func(t *testing.T) {
			f := pa327New(t, true, false)
			switch failure {
			case "pin":
				f.p.Lock.Statements[0].SHA256 = "sha256:" + strings.Repeat("0", 64)
			case "ratification":
				f.p.Lock.Ratified = &project.Ratification{By: "test-fixture-only", Proposal: "sha256:" + strings.Repeat("0", 64)}
			default:
				t.Setenv("PA327_FAIL", failure)
			}
			pa327Save(t, f)
			r := pa327Run(t, f, tc)
			pa327Assert(t, f, r, false)
			if len(r.Components.Runtime) == 0 {
				t.Fatal("model failure erased independent runtime evidence")
			}
			if failure != "transport" && (len(r.Components.Conformance) != 1 || !r.Components.Conformance[0].TransitionsChecked) {
				t.Fatal("model obligation failure erased independent checked observations")
			}
		})
	}
	// Model-only remains available even when code capability is unsupported.
	f.p.Manifest.Exhaustive = true
	pa327Save(t, f)
	r, e := verify.RunModel(context.Background(), f.p.Dir, tc)
	if e != nil || r == nil || !r.ModelOnly || r.Components != nil {
		t.Fatalf("model-only regression: %+v %v", r, e)
	}
}

func TestPythonApplicationGateFingerprints(t *testing.T) {
	t.Run("local Python", func(t *testing.T) { tc, _ := pa327Fake(t); pa327Fingerprints(t, tc, false) })
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real dependencies and TLC", func(t *testing.T) {
			tc, e := toolchain.Ensure(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			pa327Fingerprints(t, tc, true)
		})
	}
}
func pa327Fingerprints(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			f := pa327New(t, explicit, wheel)
			pa327Admit(t, f)
			before := pa327Tree(t, f.root)
			first := pa327Run(t, f, tc)
			pa327Assert(t, f, first, true)
			repeat := pa327Run(t, f, tc)
			relocated := pa327New(t, explicit, wheel)
			other := pa327Run(t, relocated, tc)
			if first.Fingerprint == "" || first.Fingerprint != repeat.Fingerprint || first.Fingerprint != other.Fingerprint {
				t.Fatalf("identical repeated/relocated inputs changed fingerprint: %s %s %s", first.Fingerprint, repeat.Fingerprint, other.Fingerprint)
			}
			for _, host := range []string{f.root, relocated.root} {
				if strings.Contains(string(pa327JSON(t, first)), host) {
					t.Fatal("portable receipt leaks host root")
				}
			}
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("gate changed original inputs")
			}
			names := []string{pa327Source, "tasks/helper.py", "tasks/neighbor.py", "tasks/next.txt", "resources/payload.txt", "tests/test_tasks.py", pa327DriverPath, pa327ModelPath, pa327Project + "/.invariant/specs/Local.tla"}
			if wheel {
				names = append(names, "requirements.txt", "deps/application.txt", "deps/constraints.txt")
			}
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					q := pa327New(t, explicit, wheel)
					b, e := os.ReadFile(filepath.Join(q.root, name))
					if e != nil {
						t.Fatal(e)
					}
					suffix := "\n"
					switch {
					case strings.HasSuffix(name, ".py"), strings.Contains(name, "requirements"), strings.HasPrefix(name, "deps/"):
						suffix = "# input identity changed\n"
					case strings.HasSuffix(name, ".tla"):
						suffix = "\\* input identity changed\n"
					}
					pa327Write(t, q.root, name, string(b)+suffix)
					r := pa327Run(t, q, tc)
					pa327Assert(t, q, r, true)
					if r.Fingerprint == first.Fingerprint {
						t.Fatalf("changed %s ignored by fingerprint", name)
					}
				})
			}
			for _, kind := range []string{"bounds", "sampling"} {
				if !explicit && kind == "sampling" {
					continue
				}
				t.Run(kind, func(t *testing.T) {
					q := pa327New(t, explicit, wheel)
					if kind == "bounds" {
						q.p.Lock.Bounds["Limit"] = "3"
					} else {
						q.p.Manifest.Components.Checks[0].Invocations[1].Sampling["seed"] = "7"
					}
					pa327Save(t, q)
					r := pa327Run(t, q, tc)
					pa327Assert(t, q, r, true)
					if r.Fingerprint == first.Fingerprint {
						t.Fatalf("changed %s ignored", kind)
					}
				})
			}
			// Fingerprints must bind evidence itself, not just outcomes/counts or a
			// cached nested fingerprint supplied alongside it. Decode to exercise the
			// same representation that external consumers and attribution round trips use.
			for _, field := range []string{"observation", "runner", "observer", "dependency"} {
				if field == "dependency" && !wheel {
					continue
				}
				t.Run("actual "+field, func(t *testing.T) {
					var top map[string]any
					if e := json.Unmarshal(pa327JSON(t, first), &top); e != nil {
						t.Fatal(e)
					}
					app := top["application"].(map[string]any)
					executions := app["executions"].([]any)
					changed := false
					for _, raw := range executions {
						x := raw.(map[string]any)
						p := x["provenance"].(map[string]any)
						switch field {
						case "observation":
							if obs, ok := x["observations"].([]any); ok && len(obs) > 0 {
								obs[0].(map[string]any)["state"].(map[string]any)["task"] = "other observed scalar"
								changed = true
							}
						case "runner":
							p["runner_sha256"] = "sha256:" + strings.Repeat("b", 64)
							changed = true
						case "observer":
							p["observer_sha256"] = "sha256:" + strings.Repeat("c", 64)
							changed = true
						case "dependency":
							d := p["dependencies"].(map[string]any)
							arts := d["artifacts"].([]any)
							arts[0].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("d", 64)
							changed = true
						}
						if changed {
							break
						}
					}
					if !changed {
						t.Fatal("missing evidence to mutate")
					}
					var r verify.Report
					if e := json.Unmarshal(pa327JSON(t, top), &r); e != nil {
						t.Fatal(e)
					}
					if verify.Fingerprint(r) == first.Fingerprint {
						t.Fatalf("actual %s ignored by fingerprint", field)
					}
				})
			}
		})
	}
}

func TestPythonApplicationGateOriginsAndReceipts(t *testing.T) {
	tc, _ := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Admit(t, f)
	r := pa327Run(t, f, tc)
	pa327Assert(t, f, r, true)
	origins := func(r *verify.Report, kind string) []string {
		t.Helper()
		var root map[string]any
		if e := json.Unmarshal(pa327JSON(t, r), &root); e != nil {
			t.Fatal(e)
		}
		components := root["components"].(map[string]any)
		out := []string{}
		rows, _ := components[kind].([]any)
		for _, row := range rows {
			v, _ := row.(map[string]any)["origin"].(string)
			out = append(out, v)
		}
		return out
	}
	for _, kind := range []string{"conformance", "runtime"} {
		if got := origins(r, kind); len(got) != 1 || got[0] != "gate" {
			t.Fatalf("missing executed origin for %s: %v", kind, got)
		}
	}
	var decoded verify.Report
	if e := json.Unmarshal(pa327JSON(t, r), &decoded); e != nil {
		t.Fatal(e)
	}
	inv := &decoded.Components.Inventory
	supplied := []verify.ComponentRuntimeResult{decoded.Components.Runtime[0].Result}
	oldConformance := string(pa327JSON(t, decoded.Components.Conformance))
	passed := decoded.Passed
	if e := decoded.AttributeComponentRuntime(inv, supplied); e != nil {
		t.Fatal(e)
	}
	if got := origins(&decoded, "runtime"); len(got) != 1 || got[0] != "supplied" {
		t.Fatal("external replacement inherited gate origin")
	}
	if string(pa327JSON(t, decoded.Components.Conformance)) != oldConformance || decoded.Passed != passed {
		t.Fatal("replacement changed independent conformance or verdict")
	}
	md := receipt.Markdown(&decoded)
	if !strings.Contains(strings.ToLower(md), "gate") || !strings.Contains(strings.ToLower(md), "supplied") {
		t.Fatal("mixed origins not explained")
	}
	before := string(pa327JSON(t, &decoded))
	var bad verify.ComponentRuntimeResult
	if e := json.Unmarshal(pa327JSON(t, supplied[0]), &bad); e != nil {
		t.Fatal(e)
	}
	bad.Invocation.Args[0] = "substituted"
	if e := decoded.AttributeComponentRuntime(inv, []verify.ComponentRuntimeResult{bad}); e == nil {
		t.Fatal("mismatched invocation accepted")
	}
	if string(pa327JSON(t, &decoded)) != before {
		t.Fatal("invalid replacement was not atomic")
	}
	if e := decoded.AttributeComponentRuntime(inv, nil); e != nil {
		t.Fatal(e)
	}
	if len(decoded.Components.Runtime) != 0 || string(pa327JSON(t, decoded.Components.Conformance)) != oldConformance {
		t.Fatal("clear changed independent evidence")
	}
	// Even reusing genuine result bytes through the supplied API cannot set a
	// new gate verdict or preserve the original row's executed-by-gate label.
	suppliedConformance := []verify.ComponentConformanceResult{decoded.Components.Conformance[0].Result}
	external := verify.Report{Bounds: decoded.Bounds, Passed: false}
	if e := external.AttributeComponentConformance(inv, suppliedConformance); e != nil {
		t.Fatal(e)
	}
	if external.Passed || origins(&external, "conformance")[0] != "supplied" {
		t.Fatal("supplied row became a gate verdict")
	}
	// Adversarial text is rendered literally in BOTH receipt modes.
	hostile := "| <script>alert(327)</script> [click](https://evil.invalid) `tick`\n<!-- evidence -->"
	for _, explicit := range []bool{true, false} {
		q := pa327New(t, explicit, false)
		literal, _ := json.Marshal(hostile)
		body := strings.Replace(pa327Test, "self.assertEqual(application.tick('pending'), 'running')", "self.fail("+string(literal)+")", 1)
		pa327Write(t, q.root, "tests/test_tasks.py", body)
		if explicit {
			q.p.Manifest.Components.Checks[0].Invocations[1].Args = append(q.p.Manifest.Components.Checks[0].Invocations[1].Args, hostile)
			pa327Save(t, q)
		}
		got := pa327Run(t, q, tc)
		pa327Assert(t, q, got, false)
		md := receipt.Markdown(got)
		for _, raw := range []string{"<script>", "<!-- evidence -->", "[click](https://evil.invalid)"} {
			if strings.Contains(md, raw) {
				t.Fatalf("active hostile evidence rendered: %q", raw)
			}
		}
		if !strings.Contains(md, "alert(327)") {
			t.Fatal("diagnostic hidden instead of safely rendered")
		}
	}
}

func TestPythonApplicationGateBatches(t *testing.T) {
	tc, log := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Write(t, f.root, pa327Source, "def advance(state):\n    return state+1\n")
	pa327Write(t, f.root, "tasks/application.py", "from .lifecycle import advance\ndef start(): return 0\ndef tick(state): return advance(state)\n")
	pa327Write(t, f.root, "tests/test_tasks.py", "import unittest\nfrom tasks import application\nclass TaskTests(unittest.TestCase):\n    def test_pass(self): self.assertEqual(application.tick(0),1)\n")
	driver := strings.Replace(pa327Driver, "        life.step(application.tick)\n        life.step(application.tick)\n        life.step(application.tick)", "        for _ in range(2001):\n            life.step(application.tick)", 1)
	pa327Write(t, f.root, pa327DriverPath, driver)
	f.p.Manifest.Components.Checks[0].Invocations[1].Sampling["steps"] = "2002"
	model := `---- MODULE Lifecycle ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLE task
vars == <<task>>
Init == task = 0
Next == \/ /\ task < 2001 /\ task' = task + 1
        \/ /\ task = 2001 /\ UNCHANGED task
Spec == Init /\ [][Next]_vars
Safe == task \in 0..2001 /\ Limit >= 1 /\ Helper
Seen == task = 2001
Bad == task' = 2002
Fair == WF_vars(Next)
Eventually == <> (task = 2001)
====
`
	pa327Write(t, f.root, pa327ModelPath, model)
	for i := range f.p.Lock.Statements {
		h, err := tla.PinHash(model, f.p.Lock.Statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		f.p.Lock.Statements[i].SHA256 = h
	}
	pa327Save(t, f)
	pa327Admit(t, f)
	r := pa327Run(t, f, tc)
	pa327Assert(t, f, r, true)
	a := pa327Application(t, r)
	found := false
	for _, x := range a.Executions {
		if x.Key.Kind == "conformance" {
			found = true
			if len(x.Observations) != 2002 {
				t.Fatalf("batched observations lost: %d", len(x.Observations))
			}
		}
	}
	if !found {
		t.Fatal("missing execution")
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	batches := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"module": "Invariant_Conformance"`) {
			batches++
		}
	}
	if batches < 2 {
		t.Fatalf("2001 distinct transitions not batched: %d checks", batches)
	}
}
