// Package pythongatetest shares the serial Python application gate suites and
// their fixtures between the ordinary package run and the real-tool opt-in.
//
// What a test here costs is its gate runs (#893). Each Run starts the Docker
// double about thirty times, and the double is a Python program, so a run
// takes over a second alone on a host and ten or more in a build's sandbox,
// where eight tests share four CPUs. Staging a fixture and reading its
// sources cost next to nothing beside that: a source's syntax is read once
// while the test's process lives. So a plan's acceptance test should make
// its assertions on as few reports as it can: check everything one run can
// show on that run's report, give a refusal that admission makes to Admit,
// which runs no gate, and run the gate again only for a case that needs
// another application or another failure.
package pythongatetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const ProjectPath = "verification/lifecycle"
const Source = "tasks/lifecycle.py"
const DriverPath = ProjectPath + "/driver.py"
const ModelPath = ProjectPath + "/.invariant/specs/Lifecycle.tla"

// Local is the module beside the model, which the model extends.
const Local = "---- MODULE Local ----\nHelper == TRUE\n====\n"
const Model = `---- MODULE Lifecycle ----
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
const Driver = `from invariant_observe import functional
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
const TestSource = `import unittest
from tasks import application
class TaskTests(unittest.TestCase):
    def test_pass(self):
        self.assertEqual(application.tick('pending'), 'running')
    def test_other(self):
        self.assertEqual(application.tick('running'), 'done')
`

// Fixture owns one mutable application tree and its verification project.
type Fixture struct {
	Root    string
	Project *project.Project
}

func Write(t *testing.T, root, name, body string) {
	t.Helper()
	f := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
func JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func Save(t *testing.T, f Fixture) {
	t.Helper()
	Write(t, f.Project.Dir, ".invariant/invariant.json", string(JSON(t, f.Project.Manifest)))
	Write(t, f.Project.Dir, ".invariant/ratified.lock", string(JSON(t, f.Project.Lock)))
}
func Ref(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}
func ID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}
func New(t *testing.T, explicit, wheel bool) Fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	p := &project.Project{Dir: filepath.Join(root, ProjectPath), Manifest: project.Manifest{Name: "sampled task lifecycle", Language: "python", Code: ".", Module: ".invariant/specs/Lifecycle.tla", Conformance: "driver.py", Existing: []string{Source}}, Lock: project.Lock{Decision: "test-fixture-only", Bounds: map[string]string{"Limit": "2"}}}
	for _, s := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}, {Name: "Seen", Kind: project.Witness}, {Name: "Bad", Kind: project.Bug, Expect: "Safe"}, {Name: "Fair", Kind: project.Fairness}, {Name: "Eventually", Kind: project.Property}} {
		// Safe rests on Helper, which the module beside the model defines, so
		// its pin covers both (#812).
		model, e := tla.Read(Model, map[string]string{"Local.tla": Local})
		if e != nil {
			t.Fatal(e)
		}
		h, e := model.PinHash(s.Name, project.Model)
		if e != nil {
			t.Fatal(e)
		}
		s.SHA256 = h
		s.Says = s.Name + " fixture obligation"
		p.Lock.Statements = append(p.Lock.Statements, s)
	}
	files := map[string]string{
		"tasks/__init__.py": "", Source: "from .helper import next_state\ndef advance(state):\n    return next_state(state)\n",
		"tasks/helper.py": "from pathlib import Path\ndef next_state(state):\n    if state == 'pending':\n        return Path(__file__).with_name('next.txt').read_text().strip()\n    return 'done'\n",
		"tasks/next.txt":  "running\n", "tasks/application.py": "from .lifecycle import advance\ndef start():\n    return 'pending'\ndef tick(state):\n    return advance(state)\n",
		"tasks/neighbor.py": "def unchecked():\n    return 17\n", "tasks/constants.py": "VALUE = 19\n", "resources/payload.txt": "unchanged resource\n",
		"tests/__init__.py": "", "tests/test_tasks.py": TestSource, DriverPath: Driver, ModelPath: Model,
		ProjectPath + "/.invariant/specs/Local.tla": Local,
		"assumptions.txt": "The local clock is monotonic.\n",
	}
	if wheel {
		files["requirements.txt"] = "-r deps/application.txt\n"
		files["deps/application.txt"] = "packaging>=24,<25\n-c constraints.txt\n"
		files["deps/constraints.txt"] = "packaging==24.2\n"
		files[ProjectPath+"/requirements.txt"] = "must-not-be-installed==0\n"
		files["tasks/helper.py"] = "from packaging.version import Version\nassert Version('1.9') < Version('1.10')\n" + files["tasks/helper.py"]
	}
	if explicit {
		unit := Ref("tests/test_tasks.py", "TaskTests.test_pass")
		driver := Ref(DriverPath, "drive")
		p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"tasks"}, Checks: []project.ComponentCheck{{Target: Ref(Source, "advance"), Tests: []project.ComponentRef{unit}, Conformance: []project.ComponentRef{driver}, Assumptions: []project.ComponentAssumption{{ID: "clock", Statement: "Clock is monotonic", Reason: "Environment premise", Source: "assumptions.txt"}}, Invocations: []project.ComponentInvocation{{ID: "unit", Kind: "test", Check: unit, Command: "python", Args: []string{"-m", "unittest", "tests.test_tasks.TaskTests.test_pass"}, WorkingDir: ".", Inputs: []string{"tasks/next.txt"}}, {ID: "sample", Kind: "conformance", Check: driver, Command: "python", Args: []string{"driver.py", "literal argument"}, WorkingDir: ProjectPath, Sampling: map[string]string{"runs": "2", "steps": "4", "seed": "1"}, Inputs: []string{ModelPath, ProjectPath + "/.invariant/specs/Local.tla", ProjectPath + "/.invariant/ratified.lock"}}}}}}
	}
	for name, body := range files {
		Write(t, root, name, body)
	}
	f := Fixture{root, p}
	Save(t, f)
	return f
}
func Tree(t *testing.T, root string) map[string]string {
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
func Admit(t *testing.T, f Fixture) {
	t.Helper()
	if e := verify.Check(f.Project.Dir); e != nil {
		t.Fatalf("sampled existing Python must be admitted: %v", e)
	}
}

// This double substitutes Docker transport and emits labelled fixture TLC
// responses, but executes the actual staged runner and collector with Python.
// It never constructs a passing Python event stream or bypasses authentication.
const docker = `import os,sys,json,subprocess,pathlib
args=sys.argv[1:]
log=os.environ['PA327_LOG']
def record(row):
    with open(log,'a') as f: f.write(json.dumps(row,sort_keys=True)+'\n')
def msg(code,text):
    print('@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@' % (code,text,code))
if args and args[0]=='rm': sys.exit(0)
if args[:2]==['image','inspect']:
    record({'kind':'image','args':args}); print('linux/amd64'); sys.exit(0)
if args==['info','--format','{{json .SecurityOptions}}']:
    print('[]'); sys.exit(0)
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

// Fake substitutes Docker transport while executing the production runner with
// local Python. It changes process environment and must only be used serially.
func Fake(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	py, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	Write(t, bin, "docker.py", docker)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	launcher := "#!/bin/sh\nexec " + quote(py) + " " + quote(filepath.Join(bin, "docker.py")) + " \"$@\"\n"
	Write(t, bin, "docker", launcher)
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
func Run(t *testing.T, f Fixture, tc toolchain.Toolchain) *verify.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r, e := verify.Run(ctx, f.Project.Dir, "", tc)
	if e != nil || r == nil {
		t.Fatalf("gate must return an actual-result receipt: report=%+v err=%v", r, e)
	}
	return r
}

type ApplicationResult struct {
	Executions  []verify.PythonApplicationExecutionResult `json:"executions"`
	Conformance []struct {
		Key    verify.PythonApplicationInvocationKey `json:"key"`
		Result json.RawMessage                       `json:"result"`
	} `json:"conformance"`
}

func Application(t *testing.T, r *verify.Report) ApplicationResult {
	t.Helper()
	var top map[string]json.RawMessage
	if e := json.Unmarshal(JSON(t, r), &top); e != nil {
		t.Fatal(e)
	}
	var a ApplicationResult
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
func Assert(t *testing.T, f Fixture, r *verify.Report, pass bool) {
	t.Helper()
	if r.Passed != pass {
		t.Fatalf("passed=%v want %v: %s", r.Passed, pass, JSON(t, r))
	}
	a := Application(t, r)
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
	if f.Project.Manifest.Components == nil {
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
		if c.Target != ID(Source, "advance") {
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
		if v.Invocation == nil || v.Invocation.ID != "sample" || v.Check != ID(DriverPath, "drive") || v.Runner == nil || v.Model == nil || v.Bounds == nil || len(v.Observations) == 0 {
			t.Fatalf("attribution incomplete: %+v", v)
		}
	}
}
