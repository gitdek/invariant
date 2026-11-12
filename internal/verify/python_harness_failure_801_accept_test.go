package verify_test

// Issue #801 is a diagnostic change, not a new evidence protocol. The first
// collector failure must be retained before a later exception, caught error,
// poisoned operation or terminating cleanup can replace it. harness_error is
// optional diagnostic text on PythonApplicationExecutionResult, normalized like
// Diagnostics and excluded from its execution fingerprint. It never authorizes
// observations or changes D-0279's rules. Preserve the #645 timeout tests,
// existing authentication tests and declared-refusal/held-call regression suites.
//
// These tests run the actual runner, collector, Go gate, attribution and receipt
// writer. Only Docker transport and TLC answers are doubles. No double supplies
// Python observations or harness diagnostics. Existing pythongatetest fixtures
// own the command grammar, flags and TLC responses; the local extension below
// implements the larger driver's read-only overlay as a separate copied tree.

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
	"github.com/gitdek/invariant/internal/verify/internal/pythongatetest"
)

const harness801Cause = "hold requires a selected call on an owned worker"

const harness801Source = `task = 'pending'
class Refused(Exception):
    def __init__(self, status_code):
        self.status_code = status_code
def reset():
    global task
    task = 'pending'
def advance():
    global task
    task = 'running' if task == 'pending' else 'done'
def tick():
    if task != 'done':
        advance()
def deny():
    raise Refused(409)
def read():
    return task
`

const harness801Driver = `from invariant_observe import Refusal, Step, explore, module
from tasks import lifecycle as app

Limit = 2

def start():
    app.reset()
    return module(app.advance, fields={'task':'task'}, timeout=10)
def tick(life, state):
    life.step(app.tick)
def deny(life, state):
    life.step(app.deny)
def read(life, state):
    life.step(app.read)
def wrap(life, depth):
    try:
        if depth:
            wrap(life, depth - 1)
        else:
            life.hold(app.tick)
    except Exception as error:
        raise RuntimeError('wrapper-%02d <script>bad801</script> [link](https://invalid.example) | tick' % depth) from error
def broken(life, state):
    if FAIL_HERE:
        print('invalid hold executed at Limit=%s' % Limit, flush=True)
        try:
            wrap(life, 26)
        except Exception as first:
            try:
                life.release()
            except Exception as later:
                raise RuntimeError('later lifecycle failure: ' + str(later)) from first
    else:
        life.step(app.read)
def drive():
    explore(start, [Step('Tick', tick),
                    Step('Denied', deny, refusal=Refusal(app.Refused, status_code=409)),
                    Step('Read', read, refusal=Refusal(app.Refused, status_code=409)),
                    Step('Broken', broken)])
if __name__ == '__main__':
    drive()
`

func harness801Fixture(t *testing.T, explicit bool, condition string) pythongatetest.Fixture {
	t.Helper()
	f := pythongatetest.New(t, explicit, false)
	f.Project.Manifest.Exhaustive = true
	pythongatetest.Write(t, f.Root, pythongatetest.Source, harness801Source)
	pythongatetest.Write(t, f.Root, "tests/test_tasks.py", `import unittest
from tasks import lifecycle as app
class TaskTests(unittest.TestCase):
    def test_pass(self):
        app.reset()
        app.tick()
        self.assertEqual(app.task, 'running')
`)
	pythongatetest.Write(t, f.Root, pythongatetest.DriverPath, strings.Replace(harness801Driver, "FAIL_HERE", condition, 1))
	model := strings.Replace(pythongatetest.Model, "Next ==", "Tick ==", 1)
	model = strings.Replace(model, "Spec ==", "Denied == FALSE\nRead == UNCHANGED task\nBroken == UNCHANGED task\nNext == Tick \\/ Denied \\/ Read \\/ Broken\nSpec ==", 1)
	pythongatetest.Write(t, f.Root, pythongatetest.ModelPath, model)
	for i := range f.Project.Lock.Statements {
		s := &f.Project.Lock.Statements[i]
		// The model extends the module beside it, so its pins cover that
		// module's definitions too (#812).
		h, err := tla.PinHashWith(model, map[string]string{"Local.tla": pythongatetest.Local}, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		s.SHA256 = h
	}
	if explicit {
		v := &f.Project.Manifest.Components.Checks[0].Invocations[1]
		v.ID = "explore-801"
		v.Sampling = map[string]string{"runs": "100", "steps": "10", "seed": "1"}
	}
	pythongatetest.Save(t, f)
	return f
}

func harness801Transport(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	tc, log := pythongatetest.Fake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(docker), "docker.py")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	replace := func(old, next string) {
		t.Helper()
		if strings.Count(body, old) != 1 {
			t.Fatalf("existing transport no longer has exactly one %q", old)
		}
		body = strings.Replace(body, old, next, 1)
	}
	replace("if '/usr/local/bin/python' in args:\n", `if '/usr/local/bin/python' in args:
    import shutil
    original_root=mounts['/src']
    overlays={guest:host for guest,host in mounts.items() if guest.startswith('/src/')}
    if overlays:
        copied_root=os.path.join(mounts['/output'],'larger-src')
        shutil.copytree(original_root,copied_root)
        for guest,host in overlays.items():
            shutil.copyfile(host,os.path.join(copied_root,guest[len('/src/'):]))
            del mounts[guest]
        mounts['/src']=copied_root
`)
	replace("result=subprocess.run([os.environ['PA327_PYTHON']]+argv,cwd=wd,env=env)", `result=subprocess.run([os.environ['PA327_PYTHON']]+argv,cwd=wd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    # Undo this transport's host-only overlay relocation before the Go
    # executor applies the normal /src, /runtime and /output normalization.
    for stream,data in ((sys.stdout,result.stdout),(sys.stderr,result.stderr)):
        stream.write(data.decode('utf-8','replace').replace(mounts['/src'],original_root))
        stream.flush()`)
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return tc, log
}

func harness801Execution(t *testing.T, r *verify.Report) *verify.PythonApplicationExecutionResult {
	t.Helper()
	if r.Application == nil {
		t.Fatal("gate omitted application results")
	}
	for i := range r.Application.Executions {
		x := &r.Application.Executions[i]
		if x.Key.Kind == "conformance" {
			return x
		}
	}
	t.Fatal("gate never executed its conformance invocation")
	return nil
}

func harness801Diagnostic(t *testing.T, x *verify.PythonApplicationExecutionResult) string {
	t.Helper()
	var wire struct {
		HarnessError string `json:"harness_error"`
	}
	if err := json.Unmarshal(pythongatetest.JSON(t, x), &wire); err != nil {
		t.Fatal(err)
	}
	return wire.HarnessError
}

func harness801Rejected(t *testing.T, x *verify.PythonApplicationExecutionResult) {
	t.Helper()
	if x.Success || x.Complete || len(x.Observations) != 0 || x.Exploration != nil || x.ObservationMode != "" {
		t.Errorf("failed stream retained accepted evidence: %+v", x)
	}
}

func harness801Receipt(t *testing.T, r *verify.Report, key verify.PythonApplicationInvocationKey) {
	t.Helper()
	out := t.TempDir()
	if err := receipt.Write(out, r); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var back verify.Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Application, back.Application) {
		t.Error("receipt JSON changed application diagnostics or invocation evidence")
	}
	md, err := os.ReadFile(filepath.Join(out, "receipt.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, failed, ok := strings.Cut(string(md), "**What failed**")
	if !ok {
		t.Error("receipt has no What failed section")
		return
	}
	failed, _, _ = strings.Cut(failed, "\n<details>")
	plain := html.UnescapeString(failed)
	for _, want := range []string{harness801Cause, key.Target, key.Check, key.ID} {
		if !strings.Contains(plain, want) {
			t.Errorf("What failed omits %q: %s", want, failed)
		}
	}
	if strings.Contains(plain, "Traceback (most recent call last)") || strings.Contains(plain, "wrapper-26") {
		t.Error("short failure summary dumped the traceback instead of naming the first cause")
	}
	for _, active := range []string{"<script>bad801</script>", "[link](https://invalid.example)"} {
		if strings.Contains(string(md), active) {
			t.Errorf("receipt rendered active application text: %s", active)
		}
	}
}

func TestPythonApplicationGateFirstHarnessFailure801(t *testing.T) {
	tc, _ := harness801Transport(t)
	for _, explicit := range []bool{true, false} {
		for _, caught := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%t/caught=%t", explicit, caught), func(t *testing.T) {
				f := harness801Fixture(t, explicit, "True")
				if caught {
					// Completely discard the Python exception. The later operation
					// must not replace the collector's remembered first failure.
					driver := strings.Replace(harness801Driver, "FAIL_HERE", "True", 1)
					start := strings.Index(driver, "        try:\n            wrap(life, 26)")
					end := strings.Index(driver[start:], "    else:\n") + start
					driver = driver[:start] + "        try:\n            life.hold(app.tick)\n        except Exception:\n            pass\n        life.release()\n" + driver[end:]
					pythongatetest.Write(t, f.Root, pythongatetest.DriverPath, driver)
				}
				before := pythongatetest.Tree(t, f.Root)
				r := pythongatetest.Run(t, f, tc)
				x := harness801Execution(t, r)
				if r.Passed {
					t.Error("invalid hold passed the gate")
				}
				harness801Rejected(t, x)
				diagnostic := strings.Join(x.Diagnostics, "\n")
				if !strings.Contains(harness801Diagnostic(t, x), harness801Cause) {
					t.Errorf("execution JSON lacks its first harness_error: %s", diagnostic)
				}
				if !strings.Contains(diagnostic, harness801Cause) || !strings.Contains(diagnostic, "poison") {
					t.Errorf("full diagnostics lost first or later failure: %s", diagnostic)
				}
				if !caught && (strings.Count(diagnostic, "\n") <= 20 || !strings.Contains(diagnostic, "wrapper-00") || !strings.Contains(diagnostic, "wrapper-26")) {
					t.Error("the actual long exception chain was not retained")
				}
				if strings.Contains(diagnostic, "application call raised an undeclared or invalid refusal") {
					t.Error("collector error was relabeled as an application refusal")
				}
				found := false
				for _, c := range r.Application.Conformance {
					if c.Key == x.Key {
						found = true
						if c.Result.Passed || len(c.Checks) != 0 || !strings.Contains(c.Result.Message, harness801Cause) {
							t.Errorf("conformance lost the execution cause or checked invalid evidence: %+v", c)
						}
					}
				}
				if !found {
					t.Error("failed invocation has no conformance result")
				}
				if r.Components != nil {
					for _, c := range r.Components.Conformance {
						if c.TransitionsChecked || c.EveryStep || c.ExhaustiveStates || c.Status != "unchecked" {
							t.Error("failed collector earned component conformance")
						}
					}
				}
				if !reflect.DeepEqual(before, pythongatetest.Tree(t, f.Root)) {
					t.Error("verification changed the original application")
				}
				if strings.Contains(diagnostic, f.Root) {
					t.Error("diagnostics exposed original application host paths")
				}
				if !strings.Contains(diagnostic, "/src/verification/lifecycle/driver.py") || !strings.Contains(diagnostic, "/runtime/") {
					t.Error("runner traceback paths were not normalized")
				}
				harness801Receipt(t, r, x.Key)
			})
		}
	}
}

func TestPythonApplicationGateLargerHarnessFailure801(t *testing.T) {
	tc, log := harness801Transport(t)
	// Positive control exercises both declared refusal and declared normal
	// return, with a real selected call elsewhere in the exploration.
	good := harness801Fixture(t, true, "False")
	r := pythongatetest.Run(t, good, tc)
	x := harness801Execution(t, r)
	if !r.Passed || !x.Success || !x.Complete || x.Exploration == nil || x.Exploration.DeclaredRefusals == nil || *x.Exploration.DeclaredRefusals != 3 {
		t.Fatalf("valid declared refusals no longer work: %s", pythongatetest.JSON(t, r))
	}
	if harness801Diagnostic(t, x) != "" {
		t.Error("successful execution has a harness failure")
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Denied") || !strings.Contains(string(b), "ENABLED") {
		t.Fatal("declared refusals never reached named-action TLC checking")
	}
	bad := harness801Fixture(t, true, "False")
	pythongatetest.Write(t, bad.Root, pythongatetest.Source, strings.Replace(harness801Source, "raise Refused(409)", "raise RuntimeError('undeclared application failure 801')", 1))
	r = pythongatetest.Run(t, bad, tc)
	if r.Passed {
		t.Error("undeclared application exception passed")
	}
	harness801Rejected(t, harness801Execution(t, r))

	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%t", explicit), func(t *testing.T) {
			f := harness801Fixture(t, explicit, "Limit > 2")
			r := pythongatetest.Run(t, f, tc)
			ordinary := harness801Execution(t, r)
			if !ordinary.Success || !ordinary.Complete || len(ordinary.Observations) == 0 {
				t.Fatal("failure one size larger erased successful ordinary execution")
			}
			if len(r.Application.Larger) != 1 || r.Application.Larger[0].Execution == nil {
				t.Fatal("larger driver never executed")
			}
			l := r.Application.Larger[0]
			x := l.Execution
			harness801Rejected(t, x)
			if r.Passed || !l.Result.Required || l.Result.Passed || l.Bounds["Limit"] != "3" || len(x.Actual.Overlays) != 1 || l.Driver.SHA256 == l.Original.SHA256 {
				t.Errorf("larger failure or its executed driver identity lost: %+v", l)
			}
			diagnostic := strings.Join(x.Diagnostics, "\n")
			if !strings.Contains(diagnostic, "invalid hold executed at Limit=3") || !strings.Contains(diagnostic, "wrapper-26") || strings.Count(diagnostic, "\n") <= 20 {
				t.Fatal("larger execution did not exercise the long failing chain")
			}
			for label, text := range map[string]string{"execution JSON": harness801Diagnostic(t, x), "larger result": l.Result.Message} {
				if !strings.Contains(text, harness801Cause) {
					t.Errorf("%s hides the first cause: %s", label, text)
				}
			}
			if r.Larger == nil || !strings.Contains(r.Larger.Message, harness801Cause) {
				t.Error("overall larger summary hides the first cause")
			}
			if strings.Contains(diagnostic, "application call raised an undeclared or invalid refusal") {
				t.Error("larger collector failure was relabeled as an application refusal")
			}
			harness801Receipt(t, r, x.Key)
		})
	}
}
