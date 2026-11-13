package verify

// #849 is scheduling, not a change to the collector or qualification rules.
// These cases use the existing Python runner and recording Docker/TLC doubles.
// They stay serial because those doubles and gate options are process globals.
// Builder validation also runs the affected suites shuffled, repeatedly and
// with -race, preserving their real-tool opt-ins and all preflight refusals.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func skipped849(t *testing.T, value any) []string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Skipped
}

// RequirePrerequisiteRecords849 checks the new report API before preparing
// Python, following the existing #393 and #793 acceptance tests. Keeping a
// reason on the wire alone cannot satisfy the execution assertions below.
func RequirePrerequisiteRecords849(t *testing.T) {
	t.Helper()
	var larger PythonApplicationLarger
	var proof PythonApplicationProof
	for _, record := range []any{&larger, &proof} {
		if err := json.Unmarshal([]byte(`{"skipped":["base conformance failed"]}`), record); err != nil {
			t.Fatal(err)
		}
		if got := skipped849(t, record); !slices.Equal(got, []string{"base conformance failed"}) {
			t.Fatalf("%T discarded the explicit reason dependent work was skipped: %v", record, got)
		}
	}
}

type ToolCounts849 struct {
	Base, Larger map[string]int
	LargerTLC    int
}

func counts849(t *testing.T, log string) ToolCounts849 {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	out := ToolCounts849{Base: map[string]int{}, Larger: map[string]int{}}
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		var row struct {
			Kind, Module string
			Limit        int
			Key          PythonApplicationInvocationKey
			Overlays     []string
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		if row.Kind == "tlc" && row.Module == "Counter" && row.Limit == 3 {
			out.LargerTLC++
		}
		if row.Kind == "python" {
			if len(row.Overlays) == 0 {
				out.Base[row.Key.ID]++
			} else {
				out.Larger[row.Key.ID]++
			}
		}
	}
	return out
}

// PrerequisiteLargerReport849 also supplies actual gate reports to receipt tests.
func PrerequisiteLargerReport849(t *testing.T, mode string, mixed bool) (*Report, ToolCounts849) {
	t.Helper()
	tc, log := pa342Fake(t)
	f := pa342New(t, true, false)
	driver := "import os\n" + pa342Driver
	driver = strings.Replace(driver, "    explore(start, STEPS)\n", `    case = os.environ.get('CASE849', '')
    if case == 'execution':
        raise RuntimeError('base exploration failed 849')
    explore(start, [Step('Inc', inc)] if case == 'steps' else STEPS)
`, 1)
	driver = strings.Replace(driver, "    life.step(service.inc)\n", `    if os.environ.get('CASE849') == 'states' and state['count'] >= 1:
        life.step(service.peek)
    else:
        life.step(service.inc)
`, 1)
	pa342Write(t, f.root, pa342DriverPath, driver)
	c := &f.p.Manifest.Components.Checks[0]
	c.Invocations[1].ID = "blocked"
	c.Invocations[1].Env = map[string]string{"CASE849": mode}
	if mixed {
		other := c.Invocations[1]
		other.ID, other.Env = "eligible", nil
		c.Invocations = append(c.Invocations, other)
	}
	if mode == "disagreement" {
		pa342Write(t, f.root, pa342Source, "from .step import next_count\n\ndef increment(count):\n    return next_count(count) if count < 2 else count\n")
	}
	pa342Save(t, f)
	r := pa342Unchanged(t, f, tc)
	return r, counts849(t, log)
}

func TestPythonApplicationGateSkipsLarger849(t *testing.T) {
	RequirePrerequisiteRecords849(t)
	for _, scenario := range []struct {
		mode  string
		mixed bool
	}{
		{"execution", false}, {"execution", true}, {"states", true},
		{"steps", true}, {"success", true}, {"disagreement", true},
	} {
		mode, mixed := scenario.mode, scenario.mixed
		name := mode + "/all"
		if mixed {
			name = mode + "/mixed"
		}
		t.Run(name, func(t *testing.T) {
			r, calls := PrerequisiteLargerReport849(t, mode, mixed)
			blocked := mode != "success" && mode != "disagreement"
			if calls.Base["blocked"] != 1 || calls.Base["unit"] != 1 || (mixed && calls.Base["eligible"] != 1) {
				t.Errorf("base and independent work did not each run once: %+v", calls)
			}
			wantBlocked, wantTLC, wantRows := 1, 1, 1
			if blocked {
				wantBlocked = 0
				if !mixed {
					wantTLC = 0
				}
			}
			if mixed {
				wantRows++
			}
			if calls.Larger["blocked"] != wantBlocked || calls.LargerTLC != wantTLC || (mixed && calls.Larger["eligible"] != 1) {
				t.Errorf("dependent tool invocation counts: %+v; blocked larger=%d, TLC=%d", calls, wantBlocked, wantTLC)
			}
			if len(r.Application.Larger) != wantRows {
				t.Fatalf("missing per-invocation larger records: %+v", r.Application.Larger)
			}
			for _, l := range r.Application.Larger {
				why := skipped849(t, l)
				if blocked && l.Key.ID == "blocked" {
					if len(why) == 0 || l.Execution != nil || l.Result.Required || l.Result.Passed || l.Bounds["Limit"] != "3" {
						t.Errorf("omitted rerun must be a reasoned skip, not execution, success or a second required failure: %+v", l)
					}
					joined := strings.ToLower(strings.Join(why, " "))
					word := map[string]string{"execution": "execution", "states": "state", "steps": "step"}[mode]
					if !strings.Contains(joined, word) {
						t.Errorf("skip does not name the failed %s prerequisite: %v", word, why)
					}
				} else if len(why) != 0 || l.Execution == nil || !l.Execution.Success || !l.Result.Required || l.Result.Passed != (mode != "disagreement") {
					t.Errorf("eligible larger execution lost its actual verdict: %+v", l)
				}
			}
			if r.Passed != (mode == "success") {
				t.Errorf("original failure or all-success behavior lost: passed=%v", r.Passed)
			}
			if blocked && !mixed && r.Larger != nil && (r.Larger.Required || r.Larger.Passed) {
				t.Errorf("all skipped larger work claimed a verdict: %+v", r.Larger)
			}
			if mode == "disagreement" && (r.Larger == nil || !r.Larger.Required || r.Larger.Passed) {
				t.Errorf("real larger disagreement stopped failing the gate: %+v", r.Larger)
			}
			if mixed && blocked {
				var good bool
				for _, row := range r.Components.Conformance {
					good = good || row.Result.Invocation.ID == "eligible" && row.ExhaustiveStates && row.EveryStep
				}
				if !good {
					t.Error("eligible sibling lost its exhaustive evidence")
				}
			}
		})
	}
}

func failContract849(t *testing.T, f pa342Fixture) {
	t.Helper()
	gp359Edit(t, f, "tests/test_contracts.py", "self.assertTrue(iskeyword('if'))", "self.assertTrue(iskeyword('counter'))")
}

// PrerequisiteProofReport849 uses sampled conformance, so scheduling cannot
// accidentally demand the observed-call route of every proof.
func PrerequisiteProofReport849(t *testing.T, mode string) (*Report, int) {
	t.Helper()
	tc, _ := pa342Fake(t)
	calls := gp359Verifier(t, false)
	f := gp359New(t, false)
	switch mode {
	case "contract", "sibling":
		failContract849(t, f)
	case "missing":
		f.p.Manifest.Components.TrustedContracts = f.p.Manifest.Components.TrustedContracts[1:]
	case "conformance", "alternate":
		gp359Edit(t, f, pa342DriverPath, "from invariant_observe", "import os\nfrom invariant_observe")
		gp359Edit(t, f, pa342DriverPath, "        life.step(service.inc)\n        life.step(service.inc)\n", "        life.step(service.inc)\n        life.step(service.inc)\n        if os.environ.get('BAD849') == '1':\n            life.step(service.inc)\n")
		c := &f.p.Manifest.Components.Checks[0]
		c.Invocations[1].Env = map[string]string{"BAD849": "1"}
		if mode == "alternate" {
			// This proof still qualifies despite both a failed alternative
			// conformance run and a failed contract it does not assume.
			gp359Edit(t, f, "tests/test_contracts.py", "self.assertEqual(json.dumps({'a': 1}), '{\"a\": 1}')", "self.assertEqual(json.dumps({'a': 1}), 'wrong')")
			other := c.Invocations[1]
			other.ID, other.Env = "alternate", nil
			c.Invocations = append(c.Invocations, other)
		}
	case "success":
	default:
		t.Fatalf("unknown proof mode %q", mode)
	}
	if mode == "sibling" {
		pa342Write(t, f.root, "counter/other.py", "from nagini_contracts.contracts import *\n\ndef advance(count: int) -> int:\n    Requires(count >= 0)\n    Ensures(Result() == count + 1)\n    return count + 1\n\ndef start():\n    return 0\n\ndef inc(count):\n    return advance(count)\n")
		gp359Edit(t, f, pa342DriverPath, "if __name__ == '__main__':\n    drive()\n", "from counter import other\n\ndef drive_other():\n    with functional(other.advance, other.start, argument='count', field='count') as life:\n        life.step(other.inc)\n        life.step(other.inc)\n\nif __name__ == '__main__':\n    import sys\n    if len(sys.argv) > 1:\n        drive_other()\n    else:\n        drive()\n")
		f.p.Manifest.Existing = append(f.p.Manifest.Existing, "counter/other.py")
		target, driver := pa342Ref("counter/other.py", "advance"), pa342Ref(pa342DriverPath, "drive_other")
		v := f.p.Manifest.Components.Checks[0].Invocations[1]
		v.ID, v.Check, v.Args = "sibling", driver, []string{"driver.py", "sibling"}
		f.p.Manifest.Components.Checks = append(f.p.Manifest.Components.Checks, project.ComponentCheck{Target: target,
			Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}}, Conformance: []project.ComponentRef{driver}, Invocations: []project.ComponentInvocation{v}})
	}
	pa342Save(t, f)
	r := pa342Unchanged(t, f, tc)
	return r, *calls
}

func assertSkippedProof849(t *testing.T, r *Report, target, reason string) {
	t.Helper()
	i := slices.IndexFunc(r.Application.Proofs, func(p PythonApplicationProof) bool { return p.Result.Target == target })
	if i < 0 {
		t.Fatalf("missing unrun proof record for %s", target)
	}
	p := r.Application.Proofs[i]
	why := skipped849(t, p)
	if len(why) == 0 || (reason != "" && !strings.Contains(strings.Join(why, " "), reason)) {
		t.Errorf("skip must name its blocking prerequisite %q: %v", reason, why)
	}
	if p.Ran || p.Result.Passed || len(p.Result.Verified) != 0 || p.Image != "" || len(p.Command) != 0 || p.Output != "" || p.Exit != -1 {
		t.Errorf("skipped proof claims a verifier execution: %+v", p)
	}
	if p.Input.Target.ID != target || p.Input.Fingerprint == "" || len(p.Input.Sources) == 0 || len(p.Input.Spans) == 0 || p.Result.Tool == nil || p.Result.Tool.Pins[InPlaceInputPin] != p.Input.Fingerprint {
		t.Errorf("skipped proof lost prepared input identity: %+v", p)
	}
	for _, row := range r.Components.Proofs {
		if row.Target == target && row.Status != "unchecked" {
			t.Errorf("unrun proof status=%s, want unchecked", row.Status)
		}
	}
	if r.Passed {
		t.Error("skip erased the original gate failure")
	}
}

func TestPythonApplicationGateSkipsProof849(t *testing.T) {
	RequirePrerequisiteRecords849(t)
	for _, mode := range []string{"contract", "conformance", "missing", "alternate", "sibling", "success"} {
		t.Run(mode, func(t *testing.T) {
			r, calls := PrerequisiteProofReport849(t, mode)
			blocked := mode == "contract" || mode == "conformance" || mode == "missing" || mode == "sibling"
			want := 1
			if blocked && mode != "sibling" {
				want = 0
			}
			if calls != want {
				t.Errorf("Nagini ran %d times, want %d", calls, want)
			}
			if r.Passed != (mode == "success") {
				t.Errorf("incorrect gate verdict %v for %s", r.Passed, mode)
			}
			if blocked {
				reason := ""
				if mode == "contract" || mode == "sibling" {
					reason = "keyword.iskeyword"
				}
				if mode == "conformance" {
					reason = "conformance"
				}
				assertSkippedProof849(t, r, pa342ID(gp359Source, "admit"), reason)
			} else if gp359Proof(t, r).Status != "proved" || !r.Application.Proofs[0].Ran || len(skipped849(t, r.Application.Proofs[0])) != 0 {
				t.Error("an eligible proof was suppressed or failed to qualify")
			}
			if mode != "conformance" && mode != "alternate" && r.Components.Conformance[0].Status != "tested" {
				t.Error("independent passing conformance lost")
			}
			if mode == "sibling" {
				found := false
				for _, p := range r.Application.Proofs {
					if p.Result.Target == pa342ID("counter/other.py", "advance") {
						found = p.Ran && p.Result.Passed && len(skipped849(t, p)) == 0
					}
				}
				if !found {
					t.Error("eligible sibling proof did not execute")
				}
			}
			if mode == "missing" && (len(gp359Proof(t, r).Result.Unaccounted) == 0 || gp359Proof(t, r).Result.Unaccounted[0].Kind != MissingContract) {
				t.Error("missing-contract refusal disappeared")
			}
		})
	}
}

// Stale recorded calls are injected by the trusted test transport, re-signed
// exactly as #793's transport tests do. Production collection is unchanged.
func staleCalls849(t *testing.T, c PythonApplicationCommand, p PythonApplicationProcess) PythonApplicationProcess {
	t.Helper()
	secret, err := os.ReadFile(filepath.Join(c.OutputDir, "authentication.key"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	changed := 0
	for _, line := range bytes.Split(bytes.TrimSpace(p.Events), []byte{'\n'}) {
		var row map[string]any
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		if err := d.Decode(&row); err != nil {
			t.Fatal(err)
		}
		if row["type"] == "authentication" {
			continue
		}
		if calls, ok := row["calls"].([]any); ok {
			for _, call := range calls {
				call.(map[string]any)["source_sha256"] = "sha256:" + strings.Repeat("0", 64)
				changed++
			}
		}
		if err := e.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if changed == 0 {
		t.Fatal("stale-call fixture changed no calls")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(out.Bytes())
	if err := e.Encode(map[string]string{"type": "authentication", "hmac_sha256": hex.EncodeToString(mac.Sum(nil))}); err != nil {
		t.Fatal(err)
	}
	p.Events = out.Bytes()
	return p
}

func TestPythonApplicationGateObservedPrerequisites849(t *testing.T) {
	RequirePrerequisiteRecords849(t)
	for _, mode := range []string{"both", "unrelated", "never", "accepted only", "stale", "stale alternate", "conformance", "caller contract", "selected route"} {
		t.Run(mode, func(t *testing.T) {
			tc, log := hold393GateFake(t)
			calls := gp359Verifier(t, false)
			fixtureMode := "both"
			if mode == "never" || mode == "accepted only" {
				fixtureMode = mode
			}
			f := calls793Fixture(t, fixtureMode)
			if mode == "unrelated" {
				gp359Edit(t, f, "tests/test_counter.py", "self.assertEqual(core.counts['active'], 0)", "self.assertEqual(core.counts['active'], 99)")
			}
			if mode == "stale" || mode == "stale alternate" {
				pythonApplicationGateOptions.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					p, err := RunPythonApplicationCommand(ctx, c)
					if err == nil && c.Key.Kind == "conformance" && c.Key.ID != "alternate" {
						p = staleCalls849(t, c, p)
					}
					return p, err
				}
				if mode == "stale alternate" {
					c := &f.p.Manifest.Components.Checks[0]
					v := c.Invocations[1]
					v.ID = "alternate"
					c.Invocations = append(c.Invocations, v)
				}
			}
			if mode == "conformance" {
				// The recorder still completes; the TLC double reports a failed
				// attempted-step obligation, not a failed tool process.
				docker, err := exec.LookPath("docker")
				if err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(filepath.Dir(docker), "docker.py")
				body, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				old := "    passed(3,3)"
				if !strings.Contains(string(body), old) {
					t.Fatal("held-call TLC fixture moved")
				}
				body = []byte(strings.Replace(string(body), old, "    if module=='Invariant_Tried':\n        msg(2110,'Invariant Invariant_EveryStepTried is violated.'); sys.exit(12)\n"+old, 1))
				if err := os.WriteFile(name, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "caller contract" {
				gp359Edit(t, f, calls793Source, "    Ensures(Implies(has_site", "    Requires(requested == 'boat')\n    Ensures(Implies(has_site")
			}
			if mode == "selected route" {
				// Same selected function has valid ordinary exhaustive conformance
				// even though its observed calls have no declared-refused coverage.
				tc, log = pa342Fake(t)
				f = gp359New(t, false)
				f.p.Manifest.Exhaustive = true
				f.p.Manifest.Components.Checks[0].Observe = []project.ComponentRef{pa342Ref(gp359Source, "admit")}
				f.p.Manifest.Components.Checks[0].Invocations[1].Sampling = map[string]string{"runs": "200", "steps": "10", "seed": "1"}
				driver := strings.ReplaceAll(pa342Driver, "increment", "admit")
				pa342Write(t, f.root, pa342DriverPath, driver)
				gp359Edit(t, f, "counter/service.py", "def inc(count):", "def peek(count):\n    return count\n\n\ndef inc(count):")
			}
			pa342Save(t, f)
			r := pa342Unchanged(t, f, tc)
			eligible := mode == "both" || mode == "unrelated" || mode == "stale alternate" || mode == "selected route"
			want := 0
			if eligible {
				want = 1
			}
			if *calls != want {
				t.Errorf("Nagini invocation count=%d, want %d", *calls, want)
			}
			if r.Passed != (mode == "both" || mode == "selected route") {
				t.Errorf("wrong gate verdict for %s: %v", mode, r.Passed)
			}
			target := pa342ID(calls793Source, "admission")
			if mode == "selected route" {
				target = pa342ID(gp359Source, "admit")
			}
			if eligible {
				found := false
				for _, p := range r.Application.Proofs {
					found = found || p.Result.Target == target && p.Ran && p.Result.Passed && len(skipped849(t, p)) == 0
				}
				if !found {
					t.Error("valid same-declaration evidence was suppressed")
				}
				for _, p := range r.Components.Proofs {
					if p.Target == target && p.Status != "proved" {
						t.Errorf("eligible proof is %s", p.Status)
					}
				}
			} else {
				assertSkippedProof849(t, r, target, "")
			}
			if mode == "both" || mode == "unrelated" {
				x, _ := pa342Explored(t, r)
				calls793Assert(t, x, 1)
			}
			if mode == "stale alternate" && counts849(t, log).Base["alternate"] != 1 {
				t.Error("alternate evidence was not collected")
			}
			if mode == "caller contract" && !strings.Contains(strings.Join(PythonApplicationProofProblems(r.Components, r.Application), " "), "Requires(requested == 'boat')") {
				t.Error("known caller-contract failure was hidden")
			}
		})
	}
}

// PrerequisiteTestCallerReport849 uses #841's real test-call recorder. The
// exploration's calls all meet the contract; only the application's test
// introduces a violation, without failing its own assertions.
func PrerequisiteTestCallerReport849(t *testing.T, mode string) (*Report, int) {
	t.Helper()
	tc, _ := hold393GateFake(t)
	calls := gp359Verifier(t, false)
	imports := "from .admission import admission\ndef allowed(requested, busy):\n    return admission(True, 'statue', requested, busy) == 0\n"
	f := callers838Fixture(t, callers838Admission, callers838Application(t, imports, callers838Call), hold393Driver)
	var body string
	switch mode {
	case "direct":
		body = "        self.assertEqual(admission(True, 'statue', 'car', False), 0)\n"
	case "application":
		body = "        self.assertTrue(core.allowed('car', False))\n"
	case "valid":
		body = "        self.assertTrue(core.allowed('engine', False))\n"
	default:
		t.Fatalf("unknown test caller mode %q", mode)
	}
	pa342Write(t, f.root, "tests/test_counter.py", tests841("", body))
	pa342Save(t, f)
	r := pa342Unchanged(t, f, tc)
	return r, *calls
}

func TestPythonApplicationGateSkipsTestCallerContract849(t *testing.T) {
	RequirePrerequisiteRecords849(t)
	for _, mode := range []string{"direct", "application", "valid"} {
		t.Run(mode, func(t *testing.T) {
			r, calls := PrerequisiteTestCallerReport849(t, mode)
			target := pa342ID(calls793Source, "admission")
			checks := callers838Checks(t, r)
			if checks.Tests != 1 || checks.Calls <= checks.Tests {
				t.Fatalf("expected both exploration and test calls: %+v", checks)
			}
			var testsPassed bool
			for _, x := range r.Application.Executions {
				if x.Key.Kind == "test" {
					testsPassed = x.Complete && x.Success && len(x.Tests) == 1 && x.Tests[0].Outcome == "passed"
				}
			}
			if !testsPassed || len(r.Application.Conformance) == 0 {
				t.Fatal("fixture must complete its tests and exploration")
			}
			for _, c := range r.Application.Conformance {
				if !c.passed() {
					t.Error("test call erased independent passing exploration conformance")
				}
			}
			if mode == "valid" {
				if calls != 1 || !r.Passed || !checks.Evidence() || calls793Proof(t, r).Status != "proved" || !r.Application.Proofs[0].Ran || len(skipped849(t, r.Application.Proofs[0])) != 0 {
					t.Errorf("valid test call suppressed a qualifying proof: Nagini calls=%d, checks=%+v", calls, checks)
				}
				return
			}
			if calls != 0 {
				t.Errorf("Nagini ran %d times despite a test's known contract violation", calls)
			}
			const clause = "Requires(requested == 'engine' or requested == 'boat')"
			const testID = "tests.test_counter.CounterTests.test_inc"
			assertSkippedProof849(t, r, target, clause)
			if len(checks.Findings) != 1 {
				t.Fatalf("want only the test call's finding, got %+v", checks.Findings)
			}
			finding := checks.Findings[0]
			caller := "tests/test_counter.py"
			if mode == "application" {
				caller = "counter/core.py"
			}
			if finding.Test != testID || finding.Caller != caller || finding.Own != (mode == "application") || !strings.Contains(finding.String(), clause) {
				t.Errorf("skip lost the test call's attribution: %+v", finding)
			}
			if why := strings.Join(skipped849(t, r.Application.Proofs[0]), " "); !strings.Contains(why, testID) || !strings.Contains(why, caller) {
				t.Errorf("skip does not name the blocking test call: %s", why)
			}
			problems := strings.Join(PythonApplicationProofProblems(r.Components, r.Application), " ")
			if !strings.Contains(problems, clause) || !strings.Contains(problems, testID) {
				t.Errorf("skipping Nagini hid the contract violation: %s", problems)
			}
		})
	}
}
