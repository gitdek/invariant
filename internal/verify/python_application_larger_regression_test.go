package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

func largerRegressionFake881(t *testing.T, extra string) (toolchain.Toolchain, string) {
	t.Helper()
	tc, log := pa342Fake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(pa342Docker, "    def passed(n,depth=None):", extra+"\n    def passed(n,depth=None):", 1)
	pa342Write(t, filepath.Dir(docker), "docker.py", body)
	return tc, log
}

func TestPythonApplicationLargerUnavailableAndCleanup881(t *testing.T) {
	for _, mode := range []string{"success", "init unavailable", "steps unavailable", "strict unavailable", "tried unavailable", "deadline", "parent cancellation", "invalid execution", "invalid recording"} {
		t.Run(mode, func(t *testing.T) {
			tc, _ := largerRegressionFake881(t, `    fault=os.environ['PA881_FAULT']
    if limit==3 and module.startswith('Invariant_'):
        if fault in ('deadline','parent cancellation'):
            import time
            time.sleep(30)
        if (fault=='init unavailable' and module=='Invariant_ConformanceInit' or
            fault=='steps unavailable' and module=='Invariant_Conformance' or
            fault=='strict unavailable' and module=='Invariant_StrictAttempts' or
            fault=='tried unavailable' and module=='Invariant_Tried'):
            print('TLC fixture unavailable'); sys.exit(73)`)
			t.Setenv("PA881_FAULT", mode)
			f := pa342New(t, true, false)
			switch mode {
			case "strict unavailable":
				driver := strings.Replace(pa342Driver, "import Step,", "import Refusal, Step,", 1)
				driver = strings.Replace(driver, "Step('Inc', inc)", "Step('Inc', inc, refusal=Refusal(ValueError))", 1)
				pa342Write(t, f.root, pa342DriverPath, driver)
			case "invalid execution":
				pa342Write(t, f.root, pa342Source, "def increment(count):\n    if count == 2:\n        raise ValueError('larger execution failed')\n    return count + 1\n")
			case "invalid recording":
				pa342Write(t, f.root, pa342Source, "def increment(count):\n    return count + (1 if count < 2 else 1.5)\n")
			}
			parent, allowance := time.Minute, time.Minute
			if mode == "deadline" {
				allowance = 5 * time.Second
			}
			if mode == "parent cancellation" {
				parent = 5 * time.Second
			}
			saved := pythonApplicationLargerTimeouts
			pythonApplicationLargerTimeouts.explore = allowance
			t.Cleanup(func() { pythonApplicationLargerTimeouts = saved })
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx, cancel := context.WithTimeout(context.Background(), parent)
			defer cancel()
			r, err := Run(ctx, f.p.Dir, "", tc)
			gate569Clean(t, tmp)
			if mode == "parent cancellation" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("parent cancellation lost: %v", err)
				}
				return
			}
			if err != nil || r == nil || len(r.Application.Larger) != 1 {
				t.Fatalf("gate: %+v %v", r, err)
			}
			_, base := pa342Explored(t, r)
			if !base.passed() {
				t.Fatalf("lost passing base: %+v", base)
			}
			l := r.Application.Larger[0]
			invalid := strings.HasPrefix(mode, "invalid")
			if invalid {
				if r.Passed || !l.Result.Required || l.Result.Passed {
					t.Fatalf("invalid evidence claimed nothing instead of failing: %+v", l)
				}
				return
			}
			if !r.Passed || l.Result.Required != (mode == "success") || l.Result.Passed != (mode == "success") || l.Conformance == nil || l.Execution == nil || !l.Execution.Success || !l.Execution.Complete {
				t.Fatalf("unavailable check or successful check misclassified: %+v", l)
			}
			if l.Result.States != 4 || l.Result.WantStates != 4 || l.Result.Depth != 4 || l.Result.WantDepth != 4 {
				t.Fatalf("completed counts lost: %+v", l.Result)
			}
		})
	}
}

func TestPythonApplicationLargerDeclaredOutcome881(t *testing.T) {
	tc, _ := largerRegressionFake881(t, `    if module=='Invariant_StrictAttempts':
        attempts=re.findall(r'<<\[count \|-> (-?\d+)\], \[count \|-> (-?\d+)\], "Inc", <<>>, FALSE>>',body)
        assert attempts, 'declared outcomes lost'
        for i,(before,after) in enumerate(attempts):
            if not (int(before)<limit and int(after)==int(before)+1):
                msg(2114,'Deadlock reached.')
                msg(2217,'1: <Initial predicate>\n/\\ count = %s\n/\\ invariant_i = %d'%(before,i+1)); sys.exit(11)`)
	f := pa342New(t, true, false)
	driver := strings.Replace(pa342Driver, "import Step,", "import Refusal, Step,", 1)
	driver = strings.Replace(driver, "Step('Inc', inc)", "Step('Inc', inc, refusal=Refusal(ValueError))", 1)
	driver = strings.Replace(driver, "if state['count'] >= Limit:\n        return", "if state['count'] >= Limit:\n        if Limit > 2:\n            life.step(service.inc)\n        return", 1)
	pa342Write(t, f.root, pa342DriverPath, driver)
	service, err := os.ReadFile(filepath.Join(f.root, "counter/service.py"))
	if err != nil {
		t.Fatal(err)
	}
	pa342Write(t, f.root, "counter/service.py", strings.Replace(string(service), "def inc(count):\n", "def inc(count):\n    if count == 3:\n        return count\n", 1))
	r := pa342Unchanged(t, f, tc)
	_, base := pa342Explored(t, r)
	if !base.passed() || len(r.Application.Larger) != 1 {
		t.Fatalf("base declaration did not pass: %s", Feedback(r))
	}
	l := r.Application.Larger[0]
	c := l.Conformance
	if r.Passed || !l.Result.Required || l.Result.Passed || l.Result.States != 4 || l.Result.Depth != 4 || c == nil || c.Result.BadStep == nil || c.Result.BadStep.From != "count = 3" || c.Result.BadStep.To != "count = 3" || !strings.Contains(l.Result.Message, "Inc") {
		t.Fatalf("normal unchanged return escaped its declared action: %+v", l)
	}
	found := false
	for _, check := range c.Checks {
		found = found || check.Module == "Invariant_StrictAttempts" && check.Result.Outcome == tlc.Deadlock && check.Config.Constants["Limit"] == "3"
	}
	if !found {
		t.Fatal("named-action counterexample missing")
	}
}

// The initializer is a named surrounding application declaration, as the
// collector requires. It reads the larger environment's initial value.
func TestPythonApplicationLargerNamedInitialState881(t *testing.T) {
	tc, _ := pa342Fake(t)
	f := pa342New(t, true, false)
	service, err := os.ReadFile(filepath.Join(f.root, "counter/service.py"))
	if err != nil {
		t.Fatal(err)
	}
	pa342Write(t, f.root, "counter/service.py", "INITIAL = 0\n"+strings.Replace(string(service), "return 0", "return INITIAL", 1))
	driver := strings.Replace(pa342Driver, "def drive():\n", "def drive():\n    service.INITIAL = 0 if Limit == 2 else -1\n", 1)
	driver = strings.Replace(driver, "state['count'] >= Limit:", "state['count'] >= Limit - (1 if Limit > 2 else 0):", 1)
	pa342Write(t, f.root, pa342DriverPath, driver)
	r := pa342Unchanged(t, f, tc)
	_, base := pa342Explored(t, r)
	if !base.passed() || len(r.Application.Larger) != 1 {
		t.Fatalf("base initializer must pass: %s", Feedback(r))
	}
	l := r.Application.Larger[0]
	if r.Passed || !l.Result.Required || l.Result.Passed || l.Result.States != 4 || l.Result.Depth != 4 || l.Result.WantStates != 4 || l.Result.WantDepth != 4 || l.Conformance == nil || l.Conformance.Result.BadStart != "count = -1" {
		t.Fatalf("equal counts hid an invalid initial state: %+v", l)
	}
	if !strings.Contains(l.Result.Message, "Init") || !strings.Contains(l.Result.Message, "-1") {
		t.Fatalf("invalid initial state not explained: %s", l.Result.Message)
	}
}

// Enough distinct attempts for multiple Next and tried windows, within the
// collector's protocol scale, without quadratic counter replay in Python.
func TestPythonApplicationLargerBatchesAndEnvironment881(t *testing.T) {
	for _, parameter := range []bool{false, true} {
		t.Run(fmt.Sprintf("parameter=%v", parameter), func(t *testing.T) {
			tc, log := largerRegressionFake881(t, `    if module=='Invariant_Tried':
        record({'kind':'environment881','body':body})`)
			f := pa342New(t, true, false)
			if parameter {
				f.p.Manifest.Parameters = []string{"Limit"}
			}
			s := &PythonApplicationSession{project: f.p, root: f.root}
			const limit = 4001
			state := func(i int) map[string]any { return map[string]any{"count": json.Number(fmt.Sprint(i))} }
			no := false
			x := &PythonApplicationExecutionResult{Success: true, Complete: true, ObservationMode: "explored",
				Exploration:  &PythonApplicationExploration{States: limit + 1, Depth: limit + 1},
				Observations: []PythonApplicationObservation{{Kind: "initial", State: state(0)}}}
			for i := 0; i <= limit; i++ {
				x.Observations = append(x.Observations, PythonApplicationObservation{Kind: "transition", Step: "Done", Before: state(i), After: state(i), Replay: &no})
				if i < limit {
					x.Observations = append(x.Observations, PythonApplicationObservation{Kind: "transition", Step: "Inc", Before: state(i), After: state(i + 1), Replay: &no})
				} else if parameter {
					x.Observations = append(x.Observations, PythonApplicationObservation{Kind: "transition", Step: "Inc", Before: state(i), After: state(i), Replay: &no})
				}
			}
			attempts := 2*limit + 1
			if parameter {
				attempts++
			}
			work, tmp := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			budget, err := tlcBudget(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			c, err := checkPythonApplicationConformance(ctx, s, x, true, work, "larger", pa342Model, map[string]string{"Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n"}, map[string]string{"Limit": fmt.Sprint(limit)}, limit+1,
				tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar, Budget: budget, CheckRoot: work})
			if err != nil || !c.passed() || c.Result.Steps != attempts || c.Result.Transitions != limit || c.Result.Tried.Attempts != attempts {
				t.Fatalf("batched recording: %+v %v", c, err)
			}
			checks := map[string]int{}
			for _, check := range c.Checks {
				checks[check.Module]++
				if check.Config.Constants["Limit"] != fmt.Sprint(limit) || check.InputSHA256 == "" {
					t.Fatalf("batch lost larger constants or input: %+v", check)
				}
			}
			if checks["Invariant_Conformance"] != 3 || checks["Invariant_Tried"] != 5 || len(c.LogicalChecks) != 9 || len(budget.Attempts()) != 9 {
				t.Fatalf("windows escaped shared budget: %v, logical=%d, budget=%d", checks, len(c.LogicalChecks), len(budget.Attempts()))
			}
			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "INSTANCE Counter WITH Limit <- 4002") == parameter || !strings.Contains(string(raw), "INSTANCE Counter") {
				t.Fatal("omissions used base bounds or enlarged a code parameter")
			}
			gate569Clean(t, tmp)
			gate569Clean(t, work)
		})
	}
}

func TestPythonApplicationLargerLogicalFingerprint881(t *testing.T) {
	r := batchReport("steps-1", 5, tlc.Deadlock)
	c := r.Application.Conformance[0]
	c.LogicalChecks = []PythonApplicationLogicalCheck{{Module: "Invariant_Conformance", Batch: "steps-1", InputSHA256: "sha256:00",
		Config: tlc.Config{Constants: map[string]string{"Limit": "3"}}, Outcome: tlc.Deadlock,
		Trace: []tlc.State{{Vars: []tlc.Var{{Name: "count", Value: "2"}}}}}}
	r.Application.Larger = []PythonApplicationLarger{{Conformance: &c}}
	fingerprint := Fingerprint(r)
	wire, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"input", "constants", "verdict", "counterexample", "scheduling"} {
		t.Run(field, func(t *testing.T) {
			var copy Report
			if err := json.Unmarshal(wire, &copy); err != nil {
				t.Fatal(err)
			}
			c := copy.Application.Larger[0].Conformance
			switch field {
			case "input":
				c.LogicalChecks[0].InputSHA256 = "changed"
			case "constants":
				c.LogicalChecks[0].Config.Constants["Limit"] = "4"
			case "verdict":
				c.LogicalChecks[0].Outcome = tlc.Passed
			case "counterexample":
				c.LogicalChecks[0].Trace[0].Vars[0].Value = "4"
			case "scheduling":
				c.Checks = nil
				c.LogicalChecks[0].Batch = "recovered/other"
			}
			if (Fingerprint(copy) == fingerprint) != (field == "scheduling") {
				t.Fatalf("fingerprint binding for %s", field)
			}
		})
	}
	if Fingerprint(r) != fingerprint || c.LogicalChecks[0].Batch != "steps-1" {
		t.Fatal("fingerprint mutated original evidence")
	}
}
