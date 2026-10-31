package verify

// Issue #342, step 3 of #313's ratified plan, built by hand (D-0195):
// exhaustive existing Python applications and applicable larger bounds.
//
// An exhaustive manifest's driver explores through the gate's own harness,
// invariant_observe.explore: every state the application reaches is read
// back from the application itself, every attempt is a bound application
// call, and TLC checks the attempts as every exhaustive driver's are
// (D-0082, D-0085, D-0088). Sampled runs never earn exhaustive labels; a
// missing state fails the exhaustive claim; a failed attempt check fails the
// gate and blocks the every-step and every-state labels but keeps checked
// transitions. When the driver names its bounds, TLC and the exploration run
// again one size larger (D-0068, D-0076), with the same snapshot and image,
// a private driver copy supplying the bounds.
//
// Default tests run the actual staged runner, collector and harness with
// local Python; Docker transport and TLC are a recording double that
// emulates this counter model. With INVARIANT_PYTHON_APPLICATION_GATE_REAL=1
// the same cases also run with real Docker, the pinned Python image, TLC and
// an installed wheel, and fail on missing prerequisites:
//
//	INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify \
//	  -run '^TestPythonApplicationGate' -count=1 -timeout 60m

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

const pa342Project = "verification/counter"
const pa342Source = "counter/core.py"
const pa342DriverPath = pa342Project + "/driver.py"
const pa342ModelPath = pa342Project + "/.invariant/specs/Counter.tla"
const pa342Model = `---- MODULE Counter ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLE count
vars == <<count>>
Init == count = 0
Inc == count < Limit /\ count' = count + 1
Done == count = Limit /\ UNCHANGED count
Next == Inc \/ Done
Spec == Init /\ [][Next]_vars
Safe == count <= Limit /\ Helper
Full == count = Limit
Overflow == count' = Limit + 1
====
`

// pa342Driver explores the counter. The environment increments at most
// Limit times, so Inc is left untried at Limit; Done is tried everywhere and
// the application refuses it, except where it's a step that changes nothing.
const pa342Driver = `from invariant_observe import Step, explore, functional
from counter.core import increment
from counter import service

Limit = 2


def start():
    return functional(increment, service.start, argument='count', field='count')


def inc(life, state):
    if state['count'] >= Limit:
        return
    life.step(service.inc)


def done(life, state):
    life.step(service.peek)


STEPS = [Step('Inc', inc), Step('Done', done)]


def drive():
    explore(start, STEPS)


if __name__ == '__main__':
    drive()
`

const pa342Core = "from .step import next_count\n\n\ndef increment(count):\n    return next_count(count)\n"

type pa342Fixture struct {
	root string
	p    *project.Project
}

func pa342Write(t *testing.T, root, name, body string) {
	t.Helper()
	f := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pa342Save(t *testing.T, f pa342Fixture) {
	t.Helper()
	m, err := json.Marshal(f.p.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	l, err := json.Marshal(f.p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	pa342Write(t, f.p.Dir, ".invariant/invariant.json", string(m))
	pa342Write(t, f.p.Dir, ".invariant/ratified.lock", string(l))
}

func pa342Ref(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}

func pa342ID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}

// pa342New makes an unchanged counter application with a sibling import, a
// resource read and, with wheel, an installed third-party dependency, and a
// separate verification project whose manifest is exhaustive unless sampled.
func pa342New(t *testing.T, explicit, wheel bool) pa342Fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	p := &project.Project{Dir: filepath.Join(root, pa342Project), Manifest: project.Manifest{Name: "explored counter", Language: "python", Code: ".",
		Module: ".invariant/specs/Counter.tla", Conformance: "driver.py", Exhaustive: true, Existing: []string{pa342Source}},
		Lock: project.Lock{Decision: "test-fixture-only", Bounds: map[string]string{"Limit": "2"}}}
	for _, s := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}, {Name: "Full", Kind: project.Witness}, {Name: "Overflow", Kind: project.Bug, Expect: "Safe"}} {
		h, err := tla.PinHash(pa342Model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		s.SHA256, s.Says = h, s.Name+" fixture obligation"
		p.Lock.Statements = append(p.Lock.Statements, s)
	}
	step := "from pathlib import Path\n\n\ndef next_count(count):\n    return count + int(Path(__file__).with_name('step.txt').read_text())\n"
	files := map[string]string{
		"counter/__init__.py": "", pa342Source: pa342Core, "counter/step.txt": "1\n",
		"counter/service.py": "from .core import increment\n\n\ndef start():\n    return 0\n\n\ndef inc(count):\n    return increment(count)\n\n\ndef peek(count):\n    return count\n",
		"counter/neighbor.py": "def unchecked():\n    return 23\n", "resources/payload.txt": "unchanged resource\n",
		"tests/__init__.py":     "",
		"tests/test_counter.py": "import unittest\nfrom counter import service\n\n\nclass CounterTests(unittest.TestCase):\n    def test_inc(self):\n        self.assertEqual(service.inc(0), 1)\n",
		pa342DriverPath:         pa342Driver, pa342ModelPath: pa342Model,
		pa342Project + "/.invariant/specs/Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n",
	}
	if wheel {
		files["requirements.txt"] = "-r deps/application.txt\n"
		files["deps/application.txt"] = "packaging>=24,<25\n-c constraints.txt\n"
		files["deps/constraints.txt"] = "packaging==24.2\n"
		step = "from packaging.version import Version\nassert Version('1.9') < Version('1.10')\n" + step
	}
	files["counter/step.py"] = step
	if explicit {
		unit := pa342Ref("tests/test_counter.py", "CounterTests.test_inc")
		driver := pa342Ref(pa342DriverPath, "drive")
		p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"counter"}, Checks: []project.ComponentCheck{{Target: pa342Ref(pa342Source, "increment"),
			Tests: []project.ComponentRef{unit}, Conformance: []project.ComponentRef{driver},
			Invocations: []project.ComponentInvocation{
				{ID: "unit", Kind: "test", Check: unit, Command: "python", Args: []string{"-m", "unittest", "tests.test_counter.CounterTests.test_inc"}, WorkingDir: ".", Inputs: []string{"counter/step.txt"}},
				{ID: "explore", Kind: "conformance", Check: driver, Command: "python", Args: []string{"driver.py"}, WorkingDir: pa342Project, Sampling: map[string]string{"runs": "200", "steps": "10", "seed": "1"},
					Inputs: []string{pa342ModelPath, pa342Project + "/.invariant/specs/Local.tla", pa342Project + "/.invariant/ratified.lock"}}}}}}
	}
	for name, body := range files {
		pa342Write(t, root, name, body)
	}
	f := pa342Fixture{root, p}
	pa342Save(t, f)
	return f
}

// pa342Docker replaces Docker transport. TLC answers are emulated from the
// counter model at the configured Limit, the attempt check included: Done
// must be tried in every state, and Inc wherever the ratified Limit allows
// it. Application runs execute the actual staged runner and collector with
// local Python, honoring read-only overlays as Docker's nested mounts do.
const pa342Docker = `import os,sys,json,subprocess,pathlib,re,shutil,tempfile,time
args=sys.argv[1:]
log=os.environ['PA342_LOG']
def record(row):
    with open(log,'a') as f: f.write(json.dumps(row,sort_keys=True)+'\n')
def msg(code,text):
    print('@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@' % (code,text,code))
if args and args[0]=='rm': sys.exit(0)
if args[:2]==['image','inspect']:
    record({'kind':'image','args':args}); print('linux/amd64'); sys.exit(0)
if args and args[0]=='info':
    record({'kind':'info'}); sys.exit(73)
mounts={}
for i,a in enumerate(args[:-1]):
    if a=='-v':
        host,guest,*rest=args[i+1].split(':'); mounts[guest]=host
fail=os.environ.get('PA342_FAIL','')
if 'tlc2.TLC' in args:
    work=mounts['/work']; module=args[-1][:-4]
    body=pathlib.Path(work,module+'.tla').read_text(); cfg=pathlib.Path(work,'Invariant.cfg').read_text()
    limit=int(re.search(r'CONSTANT Limit = (\d+)',cfg).group(1))
    record({'kind':'tlc','module':module,'limit':limit})
    msg(2262,'TLC2 Version 2.19 of 08 August 2024')
    def passed(n):
        msg(2193,'Model checking completed. No error has been found.'); msg(2199,'%d states generated, %d distinct states found, 0 states left on queue.'%(n+1,n)); msg(2194,'The depth of the complete state graph search is %d.'%n); sys.exit(0)
    if module=='Counter':
        if limit!=2 and fail=='larger-model':
            msg(2110,'Invariant Safe is violated.'); msg(2217,'1: <Initial predicate>\n/\\ count = 0'); sys.exit(12)
        if limit!=2 and fail=='larger-slow':
            time.sleep(60)
        passed(limit+1)
    if module.startswith('Witness_'):
        msg(2110,'Invariant Invariant_Never_Full is violated.'); msg(2217,'1: <Initial predicate>\n/\\ count = 2'); sys.exit(12)
    if module.startswith('Bug_'):
        msg(2110,'Invariant Safe is violated.'); msg(2217,'1: <Initial predicate>\n/\\ count = 3'); sys.exit(12)
    if module=='Invariant_Conformance':
        values=[int(v) for v in re.findall(r'count \|-> (-?\d+)',body.split('Invariant_Steps ==')[0])]
        if any(v<0 or v>limit for v in values):
            msg(2114,'Deadlock reached.'); msg(2217,'1: <Initial predicate>\n/\\ count = 0\n/\\ invariant_source = 1\n/\\ invariant_target = 2'); sys.exit(11)
    if module=='Invariant_Tried':
        states=re.findall(r'\[count \|-> (-?\d+)\]',body.split('Invariant_States ==')[1].split('Invariant_Tried ==')[0])
        sets=re.findall(r'\{([^{}]*)\}',body.split('Invariant_Tried ==')[1].split('Invariant_TInit')[0])
        for i,(c,s) in enumerate(zip(states,sets)):
            untried=[]
            if '<<"Done">>' not in s: untried.append('<<"Done">>')
            if int(c)<limit and '<<"Inc">>' not in s: untried.append('<<"Inc">>')
            if untried:
                msg(2110,'Invariant Invariant_EveryStepTried is violated.')
                msg(2217,'1: <Initial predicate>\n/\\ count = %s\n/\\ invariant_i = %d'%(c,i+1))
                print('<<"invariant-untried", {%s}>>'%', '.join(untried)); sys.exit(12)
    passed(3)
if '/usr/local/bin/python' in args:
    pos=args.index('/usr/local/bin/python'); argv=args[pos+1:]
    if '/runtime/runner.py' not in argv: record({'kind':'forbidden','args':args}); sys.exit(94)
    env={}; start=args.index('-i')+1
    for item in args[start:pos]:
        k,v=item.split('=',1); env[k]=v
    src=mounts['/src']
    overlays={g:h for g,h in mounts.items() if g.startswith('/src/')}
    root=src
    if overlays:
        root=os.path.join(tempfile.mkdtemp(),'src'); shutil.copytree(src,root)
        for g,h in overlays.items(): shutil.copyfile(h,os.path.join(root,g[len('/src/'):]))
    def mapped(s):
        if s=='/src' or s.startswith('/src/'): return root+s[len('/src'):]
        for guest,host in sorted(mounts.items(),key=lambda p:-len(p[0])):
            if s==guest or s.startswith(guest+'/'): return host+s[len(guest):]
        return s
    req=json.loads(pathlib.Path(mapped('/runtime/request.json')).read_text())
    def remap(v):
        if isinstance(v,str): return mapped(v)
        if isinstance(v,dict): return {k:remap(x) for k,x in v.items()}
        if isinstance(v,list): return [remap(x) for x in v]
        return v
    local=pathlib.Path(mounts['/output'],'local-request.json'); local.write_text(json.dumps(remap(req)))
    argv=[str(local) if a=='/runtime/request.json' else mapped(a) for a in argv]
    if sys.version_info < (3,11): argv=[a for a in argv if a!='-P']
    image=args[args.index('-i')-1]
    record({'kind':'python','key':req.get('key'),'mode':req.get('mode',''),'overlays':sorted(overlays),'env':env,'image':image,
            'network':args[args.index('--network')+1],'argv':args[pos+1:]})
    result=subprocess.run([os.environ['PA342_PYTHON']]+argv,cwd=mapped(args[args.index('-w')+1]),env=env)
    events=pathlib.Path(mounts['/output'],'events.jsonl')
    corrupt=os.environ.get('PA342_EVENTS','')
    if req.get('key',{}).get('kind')=='conformance' and (corrupt=='all' or (corrupt=='larger' and overlays)):
        events.write_text('{malformed}\n')
    sys.exit(result.returncode)
record({'kind':'forbidden','args':args}); print('unexpected Docker operation',args); sys.exit(93)
`

func pa342Fake(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	pa342Write(t, bin, "docker.py", pa342Docker)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	pa342Write(t, bin, "docker", "#!/bin/sh\nexec "+quote(py)+" "+quote(filepath.Join(bin, "docker.py"))+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PA342_LOG", log)
	t.Setenv("PA342_PYTHON", py)
	t.Setenv("PA342_FAIL", "")
	t.Setenv("PA342_EVENTS", "")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), `"kind": "forbidden"`) {
			t.Errorf("unexpected tool preparation or execution: %s", b)
		}
	})
	return toolchain.Toolchain{JavaImage: toolchain.JavaImage, TLCJar: filepath.Join(t.TempDir(), "fixture.jar"), PythonImage: toolchain.PythonImage}, log
}

// pa342Modes runs a case with local Python and the double, and with real
// Docker, Python, TLC and the installed wheel when the real suite is asked.
func pa342Modes(t *testing.T, run func(t *testing.T, tc toolchain.Toolchain, real bool)) {
	t.Run("local Python", func(t *testing.T) {
		tc, _ := pa342Fake(t)
		run(t, tc, false)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker, Python, TLC and wheel", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatalf("requested real prerequisites: %v", err)
			}
			run(t, tc, true)
		})
	}
}

func pa342Run(t *testing.T, f pa342Fixture, tc toolchain.Toolchain) *Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	r, err := Run(ctx, f.p.Dir, "", tc)
	if err != nil || r == nil {
		t.Fatalf("gate must return an actual-result receipt: report=%+v err=%v", r, err)
	}
	return r
}

func pa342Tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		out[filepath.ToSlash(rel)] = pythonApplicationHash(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pa342Explored returns the report's one explored execution and its check.
func pa342Explored(t *testing.T, r *Report) (*PythonApplicationExecutionResult, PythonApplicationConformance) {
	t.Helper()
	if r.Application == nil || len(r.Application.Conformance) != 1 {
		t.Fatalf("want one application conformance check: %+v", r.Application)
	}
	c := r.Application.Conformance[0]
	for i := range r.Application.Executions {
		if x := &r.Application.Executions[i]; x.Key == c.Key {
			return x, c
		}
	}
	t.Fatalf("no execution for %+v", c.Key)
	return nil, c
}

func pa342Row(t *testing.T, r *Report) ComponentConformanceEvidence {
	t.Helper()
	if r.Components == nil || len(r.Components.Conformance) != 1 {
		t.Fatalf("want one component conformance row: %+v", r.Components)
	}
	return r.Components.Conformance[0]
}

func TestPythonApplicationGateExplores(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		for _, explicit := range []bool{true, false} {
			t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
				f := pa342New(t, explicit, real)
				if err := Check(f.p.Dir); err != nil {
					t.Fatalf("exhaustive application must be admitted: %v", err)
				}
				before := pa342Tree(t, f.root)
				r := pa342Run(t, f, tc)
				if !r.Passed {
					b, _ := json.MarshalIndent(r, "", " ")
					t.Fatalf("complete bounded exploration failed: %s", b)
				}
				x, c := pa342Explored(t, r)
				want := PythonApplicationExploration{Runs: 6, States: 3, Depth: 3, Attempts: 5, Refusals: 3, Omitted: 1}
				got := *x.Exploration
				got.Steps = nil
				if x.ObservationMode != "explored" || !reflect.DeepEqual(got, want) {
					t.Fatalf("exploration = %s %+v, want %+v", x.ObservationMode, got, want)
				}
				steps := x.Exploration.Steps
				if len(steps) != 2 || steps[0].Name != "Inc" || steps[0].Entry != pa342ID("counter/service.py", "inc") || steps[1].Name != "Done" || steps[1].Entry != pa342ID("counter/service.py", "peek") {
					t.Fatalf("declared steps and their entries: %+v", steps)
				}
				if c.Mode != "bounded-exhaustive" || !c.Result.Passed || !c.Result.Exhaustive || c.Result.States != 3 || c.Result.ModelStates != 3 || c.Result.Tried == nil || !c.Result.Tried.Passed || c.Result.Tried.Attempts != 5 || !c.passed() {
					t.Fatalf("explored conformance: %+v", c)
				}
				l := r.Larger
				if l == nil || !l.Required || !l.Passed || l.States != 4 || l.Depth != 4 || l.WantStates != 4 || l.Bounds["Limit"] != "3" {
					t.Fatalf("one size larger: %+v", l)
				}
				if len(r.Application.Larger) != 1 {
					t.Fatalf("larger runs: %+v", r.Application.Larger)
				}
				lr := r.Application.Larger[0]
				if lr.Driver.Path != pa342DriverPath || lr.Original.Path != pa342DriverPath || lr.Driver.SHA256 == lr.Original.SHA256 || lr.Bounds["Limit"] != "3" ||
					lr.Execution == nil || lr.Execution.Exploration == nil || lr.Execution.Exploration.States != 4 || len(lr.Execution.Actual.Overlays) != 1 || lr.Execution.Actual.Overlays[0] != (PythonApplicationOverlayFile{Path: pa342DriverPath, SHA256: lr.Driver.SHA256}) {
					t.Fatalf("larger run evidence: %+v", lr)
				}
				for _, o := range lr.Execution.Observations {
					if o.DriverSHA256 != lr.Driver.SHA256 {
						t.Fatal("larger observations aren't bound to the driver copy that ran")
					}
				}
				for _, o := range x.Observations {
					if o.DriverSHA256 == lr.Driver.SHA256 {
						t.Fatal("ordinary observations bound to the larger driver copy")
					}
				}
				if explicit {
					row := pa342Row(t, r)
					if row.Status != "tested" || !row.TransitionsChecked || !row.ExhaustiveStates || !row.EveryStep || row.Result.Mode != "bounded-exhaustive" || row.Result.Runs != 6 || row.Result.Attempts == nil || row.Result.Attempts.Outcome != "passed" {
						t.Fatalf("component conformance qualifications: %+v", row)
					}
					for _, o := range row.Result.Observations {
						if o.Action != "Inc" && o.Action != "Done" {
							t.Fatalf("attempt observation names no model step: %+v", o)
						}
					}
				} else if r.Components != nil {
					t.Fatal("omitted components invented an inventory")
				}
				if r.Code != nil || r.Toolchain.NaginiRecipe != "" || r.Claim() == "proved at every size" || r.EverySize() {
					t.Fatalf("exploration claimed proof or every size: %+v", r.Code)
				}
				if after := pa342Tree(t, f.root); !reflect.DeepEqual(before, after) {
					t.Fatal("exploration changed the application or its driver")
				}
			})
		}
	})
}

// The exploration's every execution, ordinary and larger, runs offline in
// the session's one image with the gate's startup settings.
func TestPythonApplicationGateExplorationExecutions(t *testing.T) {
	tc, log := pa342Fake(t)
	f := pa342New(t, true, false)
	r := pa342Run(t, f, tc)
	if !r.Passed {
		t.Fatal("exploration failed")
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var runs, larger int
	images := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var row struct {
			Kind     string            `json:"kind"`
			Mode     string            `json:"mode"`
			Overlays []string          `json:"overlays"`
			Env      map[string]string `json:"env"`
			Image    string            `json:"image"`
			Network  string            `json:"network"`
			Argv     []string          `json:"argv"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.Kind != "python" {
			continue
		}
		runs++
		images[row.Image] = true
		if row.Env["PYTHONHASHSEED"] != "0" || row.Network != "none" || !reflect.DeepEqual(row.Argv[:6], []string{"-S", "-s", "-P", "-B", "/runtime/runner.py", "/runtime/request.json"}) {
			t.Fatalf("execution lost the gate's startup settings: %+v", row)
		}
		if len(row.Overlays) > 0 {
			larger++
			if row.Mode != "explored" || !reflect.DeepEqual(row.Overlays, []string{"/src/" + pa342DriverPath}) {
				t.Fatalf("larger run overlays: %+v", row)
			}
		}
	}
	if runs != 3 || larger != 1 || len(images) != 1 {
		t.Fatalf("want a test run, an exploration and one larger exploration in one image: runs=%d larger=%d images=%v", runs, larger, images)
	}
}
