package verify

// These cases run the existing staged Python runner and authenticated
// collector. Only Docker transport and TLC are doubled, as in the existing
// application-gate suite. The double evaluates the counter's Init and Next
// against the actual generated checking module and its configured Limit.
// Process-global gate settings keep these tests serial.
import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

func largerFake881(t *testing.T, mode string) (toolchain.Toolchain, string) {
	t.Helper()
	tc, log := pa342Fake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(filepath.Dir(docker), "docker.py")
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	needle := "    msg(2262,'TLC2 Version 2.19 of 08 August 2024')\n"
	if strings.Count(string(raw), needle) != 1 {
		t.Fatal("counter-model fake no longer has its TLC result boundary")
	}
	extra := `    if module.startswith('Invariant_'):
        record({'kind':'larger881-obligation','module':module,'limit':limit,'body':body,'cfg':cfg})
    if limit==3 and module=='Invariant_ConformanceInit' and os.environ.get('LARGER881_MODE')=='timeout':
        import time
        time.sleep(30)
    if module=='Invariant_ConformanceInit':
        starts=[int(v) for v in re.findall(r'count \|-> (-?\d+)',body.split('Invariant_IInit ==')[0])]
        assert starts, 'Init check lost the recorded starting states'
        for value in starts:
            if value != 0:
                msg(2110,'Invariant Init is violated.')
                msg(2217,'1: <Initial predicate>\n/\\ count = %d'%value)
                sys.exit(12)
    if module=='Invariant_Conformance':
        values=[int(v) for v in re.findall(r'count \|-> (-?\d+)',body.split('Invariant_Steps ==')[0])]
        pairs=re.findall(r'<<(\d+), (\d+)>>',body.split('Invariant_Steps == {')[1].split('}',1)[0])
        assert values and pairs, 'Next check lost the recorded steps'
        for source,target in pairs:
            before,after=values[int(source)-1],values[int(target)-1]
            allowed=(before < limit and after == before+1) or (before == limit and after == before)
            if not allowed:
                msg(2114,'Deadlock reached.')
                msg(2217,'1: <Initial predicate>\n/\\ count = %d\n/\\ invariant_source = %s\n/\\ invariant_target = %s'%(before,source,target))
                sys.exit(11)
`
	if err := os.WriteFile(filename, []byte(strings.Replace(string(raw), needle, needle+extra, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARGER881_MODE", mode)
	return tc, log
}

func largerFixture881(t *testing.T, mode string, explicit bool) pa342Fixture {
	t.Helper()
	f := pa342New(t, explicit, false)
	switch mode {
	case "wrong step":
		pa342Write(t, f.root, pa342Source, "def increment(count):\n    return count + (1 if count < 2 else 2)\n")
	case "wrong start":
		// At the base this is the usual 0,1,2 exploration. One size larger
		// it starts outside Init and visits -1,0,1,2: still four levels.
		// The other start is a function of the staged application, as an
		// initializer has to be: the collector refuses one written in the
		// driver (D-0183, D-0451), so a lambda here never reached the check.
		pa342Edit(t, f, "counter/service.py", "def inc(", "def below():\n    return -1\n\n\ndef inc(")
		driver := strings.Replace(pa342Driver, "service.start, argument=", "service.start if Limit == 2 else service.below, argument=", 1)
		driver = strings.Replace(driver, "state['count'] >= Limit:", "state['count'] >= Limit - (1 if Limit > 2 else 0):", 1)
		pa342Write(t, f.root, pa342DriverPath, driver)
	case "untried":
		driver := strings.Replace(pa342Driver, "def done(life, state):\n", "def done(life, state):\n    if state['count'] == 3:\n        return\n", 1)
		pa342Write(t, f.root, pa342DriverPath, driver)
	case "good", "timeout":
	default:
		t.Fatalf("unknown fixture mode %q", mode)
	}
	return f
}

// Exported for the external-package receipt acceptance below.
func LargerCounterReport881(t *testing.T, mode string, explicit bool) *Report {
	t.Helper()
	tc, _ := largerFake881(t, mode)
	f := largerFixture881(t, mode, explicit)
	return pa342Unchanged(t, f, tc)
}

func largerRecord881(t *testing.T, r *Report) PythonApplicationLarger {
	t.Helper()
	x, base := pa342Explored(t, r)
	if !x.Success || !x.Complete || !base.passed() || base.Bounds["Limit"] != "2" || base.Result.States != 3 {
		t.Fatalf("larger checking lost passing base evidence: execution=%+v conformance=%+v", x, base)
	}
	if r.Larger == nil || len(r.Application.Larger) != 1 {
		t.Fatalf("missing larger result: %+v", r.Application.Larger)
	}
	l := r.Application.Larger[0]
	if l.Execution == nil || !l.Execution.Success || !l.Execution.Complete || l.Execution.Exploration == nil || l.Execution.Exploration.States != 4 || l.Execution.Exploration.Depth != 4 {
		t.Fatalf("fixture did not complete with the same four states and levels: %+v", l.Execution)
	}
	if l.Bounds["Limit"] != "3" || l.Result.States != 4 || l.Result.WantStates != 4 || l.Result.Depth != 4 || l.Result.WantDepth != 4 {
		t.Fatalf("larger result must retain its equal counts, including on conformance failure: %+v", l)
	}
	return l
}

func largerConformance881(t *testing.T, l PythonApplicationLarger) PythonApplicationConformance {
	t.Helper()
	// Decode the intended additive report API so this acceptance runs on the
	// original checkout and fails as an assertion, not an absent field error.
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Conformance *PythonApplicationConformance `json:"conformance"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Conformance == nil {
		t.Fatal("larger result has no evidence that TLC checked its authenticated attempts")
	}
	c := *wire.Conformance
	if c.Key != l.Key || c.Bounds["Limit"] != "3" || c.Mode != "bounded-exhaustive" || c.Result.ModelStates != 4 {
		t.Fatalf("larger conformance has the wrong invocation or bounds: %+v", c)
	}
	if c.Model.Module == "" || len(c.Model.Inputs) == 0 || len(c.Checks) == 0 || len(c.LogicalChecks) == 0 {
		t.Fatalf("larger conformance omitted its checked model or TLC evidence: %+v", c)
	}
	for _, check := range c.Checks {
		if check.Config.Constants["Limit"] != "3" || check.InputSHA256 == "" {
			t.Fatalf("larger attempts were checked without their larger constants and source: %+v", check)
		}
	}
	return c
}

func TestPythonApplicationLargerRejectsEqualCountWrongStep881(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			good := LargerCounterReport881(t, "good", explicit)
			goodLarger := largerRecord881(t, good)
			if !good.Passed || !good.Larger.Passed || !goodLarger.Result.Passed {
				t.Fatalf("correct counter must agree at Limit=3: %+v", good.Larger)
			}
			bad := LargerCounterReport881(t, "wrong step", explicit)
			l := largerRecord881(t, bad)
			if bad.Passed || bad.Larger.Passed || !bad.Larger.Required || l.Result.Passed || !l.Result.Required {
				t.Fatalf("equal counts hid Inc from count 2 to count 4: passed=%v larger=%+v", bad.Passed, bad.Larger)
			}
			c := largerConformance881(t, l)
			if c.Result.Passed || c.Result.BadStep == nil || !strings.Contains(c.Result.BadStep.From, "2") || !strings.Contains(c.Result.BadStep.To, "4") {
				t.Fatalf("larger counterexample lost the offending transition: %+v", c.Result)
			}
			for _, want := range []string{"Inc", "count", "2", "4"} {
				if !strings.Contains(l.Result.Message, want) || !strings.Contains(bad.Larger.Message, want) || !strings.Contains(Feedback(bad), want) {
					t.Errorf("larger failure and feedback omit %q: %+v", want, bad.Larger)
				}
			}
			if c := largerConformance881(t, goodLarger); !c.passed() {
				t.Fatalf("correct larger attempts did not pass every conformance obligation: %+v", c)
			}
		})
	}
}

func TestPythonApplicationLargerChecksInitialStates881(t *testing.T) {
	r := LargerCounterReport881(t, "wrong start", true)
	l := largerRecord881(t, r)
	if r.Passed || !l.Result.Required || l.Result.Passed {
		t.Fatalf("equal counts hid a larger initial state outside Init: %+v", l.Result)
	}
	c := largerConformance881(t, l)
	if c.Result.Passed || !strings.Contains(c.Result.BadStart, "-1") || !strings.Contains(l.Result.Message, "Init") || !strings.Contains(l.Result.Message, "-1") {
		t.Fatalf("invalid larger start not explained: conformance=%+v larger=%+v", c.Result, l.Result)
	}
}

func TestPythonApplicationLargerChecksEveryStep881(t *testing.T) {
	r := LargerCounterReport881(t, "untried", true)
	l := largerRecord881(t, r)
	if r.Passed || !l.Result.Required || l.Result.Passed {
		t.Fatalf("equal counts hid the enabled Done step never tried at 3: %+v", l.Result)
	}
	c := largerConformance881(t, l)
	if !c.Result.Passed || c.Result.Tried == nil || c.Result.Tried.Passed || !strings.Contains(c.Result.Tried.Untried, "Done") || !strings.Contains(l.Result.Message, "Done") {
		t.Fatalf("larger attempt coverage lost its independent verdict: %+v", c.Result)
	}
}

func TestPythonApplicationLargerConformanceDeadline881(t *testing.T) {
	saved := pythonApplicationLargerTimeouts
	pythonApplicationLargerTimeouts.explore = 5 * time.Second
	t.Cleanup(func() { pythonApplicationLargerTimeouts = saved })
	tc, log := largerFake881(t, "timeout")
	f := largerFixture881(t, "timeout", true)
	start := time.Now()
	r := pa342Unchanged(t, f, tc)
	elapsed := time.Since(start)
	_, base := pa342Explored(t, r)
	if !base.passed() || !r.Passed || r.Larger == nil || r.Larger.Passed || r.Larger.Required {
		t.Fatalf("unfinished larger conformance must claim nothing: passed=%v larger=%+v", r.Passed, r.Larger)
	}
	if len(r.Application.Larger) != 1 || r.Application.Larger[0].Result.Passed || r.Application.Larger[0].Result.Required {
		t.Fatalf("unfinished invocation claims agreement: %+v", r.Application.Larger)
	}
	if x := r.Application.Larger[0].Execution; x == nil || !x.Success || !x.Complete {
		t.Fatalf("fixture must time out checking completed observations, not running Python: %+v", x)
	}
	message := strings.ToLower(r.Larger.Message)
	if !strings.Contains(message, "conformance") || !strings.Contains(message, "larger") || !(strings.Contains(message, "finish") || strings.Contains(message, "deadline") || strings.Contains(message, "timed out")) {
		t.Fatalf("unfinished larger conformance is not explained: %q", r.Larger.Message)
	}
	// The fake waits thirty seconds if checking has escaped the allowance.
	if elapsed >= 20*time.Second {
		t.Fatalf("larger conformance escaped its five-second allowance: %s", elapsed)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var event struct {
			Kind, Module string
			Limit        int
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		seen = seen || event.Kind == "larger881-obligation" && event.Module == "Invariant_ConformanceInit" && event.Limit == 3
	}
	if !seen {
		t.Fatal("no larger conformance was attempted before the timeout verdict")
	}
}
