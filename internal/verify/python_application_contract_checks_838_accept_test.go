package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// callers838Admission is the statue service's refusal policy with a
// contract that asks something of its callers.
const callers838Admission = `from nagini_contracts.contracts import *

def admission(has_site: bool, site: str, requested: str, busy: bool) -> int:
    Requires(requested == 'engine' or requested == 'boat')
    Requires(Implies(has_site, site != ''))
    Ensures(Implies(busy, Result() == 409))
    Ensures(Implies(not busy, Result() == 0))
    if busy:
        return 409
    return 0
`

// callers838Application is the hold fixture's application with its busy
// check replaced by lines that call admission.
func callers838Application(t *testing.T, imports, call string) string {
	t.Helper()
	source := imports + hold393Source
	out := strings.Replace(source, "if counts['active'] != 0:\n            raise Refused(409)", call, 1)
	if out == source {
		t.Fatal("the fixture's busy check moved")
	}
	return out
}

const callers838Call = "status = admission(True, 'statue', 'engine', counts['active'] != 0)\n        if status != 0:\n            raise Refused(status)"

// callers838Fixture is the caller fixture with admission's module replaced.
func callers838Fixture(t *testing.T, admission, application, driver string) pa342Fixture {
	t.Helper()
	f := callerFixture(t, application, driver, nil)
	pa342Write(t, f.root, calls793Source, admission)
	return f
}

func callers838Checks(t *testing.T, r *Report) *PythonContractChecks {
	t.Helper()
	checks := PythonApplicationContractChecks(r.Components, r.Application, pa342ID(calls793Source, "admission"))
	if checks == nil {
		t.Fatalf("the report holds no contract to check admission's calls against: %s", gp359Dump(t, r))
	}
	return checks
}

// The gate checks a proved function's contract on every call of it that
// exploration recorded (#838). An ordinary caller that imports the
// function, and one that reaches it through another name and a value, are
// checked without a change to either; a passing application keeps its
// behavior and its proof.
func TestPythonApplicationGateChecksCallersAgainstContracts838(t *testing.T) {
	alias := callers838Application(t, "from .admission import admission as admit\npolicy = {'check': admit}\n",
		"status = policy['check'](True, 'statue', 'engine', counts['active'] != 0)\n        if status != 0:\n            raise Refused(status)")
	for _, c := range []struct{ name, application string }{
		{"an ordinary imported caller", callers838Application(t, "from .admission import admission\n", callers838Call)},
		{"another name and a function value", alias},
	} {
		run := func(t *testing.T, tc toolchain.Toolchain, real bool) {
			gp359Verifier(t, real)
			f := callers838Fixture(t, callers838Admission, c.application, hold393Driver)
			if err := Check(f.p.Dir); err != nil {
				t.Fatalf("check: %v", err)
			}
			r := pa342Unchanged(t, f, tc)
			proof := calls793Proof(t, r)
			if !r.Passed || proof.Status != "proved" {
				t.Fatalf("callers that meet the contract didn't pass: %s", strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n"))
			}
			x, _ := pa342Explored(t, r)
			hold393Assert(t, x)
			checks := callers838Checks(t, r)
			// Each call settles four parameter types, two Requires, two
			// Ensures and the result's type.
			if !checks.Evidence() || checks.Calls < 2 || checks.Own != checks.Calls || checks.Clauses != 9*checks.Calls {
				t.Fatalf("checked calls: %+v", checks)
			}
			for _, call := range callers(r) {
				if call == 0 {
					t.Fatal("a call lost its caller")
				}
			}
		}
		t.Run(c.name, func(t *testing.T) {
			t.Run("actual Python and verifier doubles", func(t *testing.T) {
				tc, _ := hold393GateFake(t)
				run(t, tc, false)
			})
			if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
				t.Run("pinned Python TLC and Nagini", func(t *testing.T) {
					tc, err := toolchain.Ensure(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					run(t, tc, true)
				})
			}
		})
	}
}

// A recorded call that doesn't meet the contract fails the gate, with the
// caller's file, the clause, and the arguments and result recorded (#838).
// The verifier double takes every proof, as a prover that models Python
// apart from the interpreter would, so the wrong result, the changed
// argument and the undeclared exception are found by the check alone.
func TestPythonApplicationGateFailsCallsThatBreakContracts838(t *testing.T) {
	imports := "from .admission import admission\n"
	head := "from typing import List\nfrom nagini_contracts.contracts import *\n\n"
	// An entry may let only its declared refusal reach it, so the
	// application's own helper turns admission's exception into a status.
	catching := imports + "def admit(busy):\n    try:\n        return admission(busy)\n    except Exception:\n        return 409\n"
	admit := "status = admit(counts['active'] != 0)\n        if status != 0:\n            raise Refused(status)"
	for _, c := range []struct {
		name, admission, imports, call string
		want                           []string
	}{
		{"a bad precondition", callers838Admission, imports,
			strings.Replace(callers838Call, "'engine'", "'car'", 1),
			[]string{"a call from counter/core.py didn't meet Requires(requested == 'engine' or requested == 'boat'), with (busy=False, has_site=True, requested=\"car\", site=\"statue\") returning 0"}},
		{"an argument of another type", callers838Admission, imports,
			strings.Replace(callers838Call, "True, 'statue'", "1, 'statue'", 1),
			[]string{"a call from counter/core.py didn't meet has_site: bool: has_site was 1"}},
		{"a wrong result", strings.Replace(callers838Admission, "Ensures(Implies(busy, Result() == 409))", "Ensures(Result() == 0)", 1), imports, callers838Call,
			[]string{"a call from counter/core.py didn't meet Ensures(Result() == 0), with (busy=True, has_site=True, requested=\"engine\", site=\"statue\") returning 409"}},
		{"an argument changed against the contract", head + `def admission(busy: bool, seen: List[int]) -> int:
    Requires(Acc(list_pred(seen)))
    Ensures(Acc(list_pred(seen)) and len(seen) == Old(len(seen)))
    seen.append(1)
    if busy:
        return 409
    return 0
`, imports, "status = admission(counts['active'] != 0, [7])\n        if status != 0:\n            raise Refused(status)",
			[]string{"a call from counter/core.py didn't meet Ensures(Acc(list_pred(seen)) and len(seen) == Old(len(seen))), with (busy=False, seen=[7]) returning 0"}},
		{"an exception the contract doesn't declare", head + `def admission(busy: bool) -> int:
    Ensures(Result() == 0)
    if busy:
        raise Exception()
    return 0
`, catching, admit,
			[]string{"a call from counter/core.py didn't meet raises only what its Exsures declare: it raised builtins.Exception, with (busy=True) raising builtins.Exception"}},
		{"a declared exception whose condition fails", head + `def admission(busy: bool) -> int:
    Exsures(Exception, not busy)
    if busy:
        raise Exception()
    return 0
`, catching, admit,
			[]string{"a call from counter/core.py didn't meet Exsures(Exception, not busy), with (busy=True) raising builtins.Exception"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tc, _ := hold393GateFake(t)
			gp359Verifier(t, false)
			f := callers838Fixture(t, c.admission, callers838Application(t, c.imports, c.call), hold393Driver)
			if err := Check(f.p.Dir); err != nil {
				t.Fatalf("check: %v", err)
			}
			r := pa342Unchanged(t, f, tc)
			x, conformance := pa342Explored(t, r)
			hold393Assert(t, x)
			proof := calls793Proof(t, r)
			if !conformance.passed() || proof.Result == nil || !proof.Result.Passed {
				t.Fatalf("the fixture must conform and its proof attempt succeed: %s", gp359Dump(t, r))
			}
			problems := strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n")
			if r.Passed || proof.Status != Unqualified {
				t.Fatalf("a call that breaks the contract passed: passed=%v status=%s\n%s", r.Passed, proof.Status, problems)
			}
			for _, want := range c.want {
				if !strings.Contains(problems, want) {
					t.Errorf("the problems don't say\n  %s\nthey say\n  %s", want, problems)
				}
			}
			if checks := callers838Checks(t, r); checks.Evidence() || checks.Own == 0 {
				t.Errorf("a broken contract still counted as evidence: %+v", checks)
			}
		})
	}
}

// A call that raises what the contract declares is checked like any other,
// and counts as the application running the function (#838).
func TestPythonApplicationGateChecksDeclaredExceptions838(t *testing.T) {
	tc, _ := hold393GateFake(t)
	gp359Verifier(t, false)
	admission := `from nagini_contracts.contracts import *

def admission(busy: bool) -> int:
    Ensures(Result() == 0)
    Exsures(Exception, busy)
    if busy:
        raise Exception()
    return 0
`
	// An entry may let only its declared refusal reach it, so the
	// application's own helper turns admission's exception into a status.
	imports := "from .admission import admission\ndef admit(busy):\n    try:\n        return admission(busy)\n    except Exception:\n        return 409\n"
	call := "status = admit(counts['active'] != 0)\n        if status != 0:\n            raise Refused(status)"
	f := callers838Fixture(t, admission, callers838Application(t, imports, call), hold393Driver)
	if err := Check(f.p.Dir); err != nil {
		t.Fatalf("check: %v", err)
	}
	r := pa342Unchanged(t, f, tc)
	if !r.Passed || calls793Proof(t, r).Status != "proved" {
		t.Fatalf("a declared exception didn't pass: %s", strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n"))
	}
	raised, returned := 0, 0
	for _, x := range r.Application.Executions {
		for _, row := range x.Observations {
			for _, call := range row.Calls {
				switch {
				case call.Raised == nil:
					returned++
				case call.Raised.Type == "builtins.Exception" && strings.Join(call.Raised.Builtins, " ") == "BaseException Exception" && row.Outcome == "refused":
					raised++
				default:
					t.Fatalf("a raising call was recorded as %+v on a %s attempt", call.Raised, row.Outcome)
				}
			}
		}
	}
	if checks := callers838Checks(t, r); raised == 0 || returned == 0 || !checks.Evidence() {
		t.Fatalf("%d raising and %d returning calls recorded: %+v", raised, returned, checks)
	}
}

// No exercised call gives no evidence that callers meet the contract, and a
// call only a driver makes is checked and counts for nothing (#838).
func TestPythonApplicationGateNeedsExercisedCallers838(t *testing.T) {
	hooked := strings.Replace(hold393Source, "async def submit(value, *, hold='forwarded'):\n    assert value == 1 and hold == 'forwarded'\n",
		"async def submit(value, *, hold='forwarded', hook=None):\n    assert value == 1 and hold == 'forwarded'\n    if hook is not None:\n        hook()\n", 1)
	driver := strings.Replace(hold393Driver, "from counter import core as app\n", "from counter import core as app\nfrom counter.admission import admission\n", 1)
	driver = strings.Replace(driver, "life.hold(app.submit, value, hold='forwarded',",
		"life.hold(app.submit, value, hold='forwarded', hook=lambda: admission(True, 'statue', 'car', state['active'] != 0),", 1)
	if hooked == hold393Source || driver == hold393Driver {
		t.Fatal("the fixture moved")
	}
	for _, c := range []struct {
		name, application, driver string
		calls                     bool
		want                      string
	}{
		{"the application never calls it", hold393Source, hold393Driver, false, ""},
		{"only the driver calls it, against the contract", hooked, driver, true,
			"a call from verification/counter/driver.py, which isn't the application's own code, didn't meet Requires(requested == 'engine' or requested == 'boat')"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tc, _ := hold393GateFake(t)
			gp359Verifier(t, false)
			f := callers838Fixture(t, callers838Admission, c.application, c.driver)
			r := pa342Unchanged(t, f, tc)
			checks := callers838Checks(t, r)
			problems := strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n")
			if r.Passed || checks.Evidence() || checks.Own != 0 || (checks.Calls > 0) != c.calls || !strings.Contains(problems, c.want) {
				t.Fatalf("passed=%v checks=%+v\n%s", r.Passed, checks, problems)
			}
		})
	}
}

// Calls that each meet the contract say nothing about the order the
// application makes them in: a run whose every call of admission is valid,
// and whose steps break the model, still fails conformance (#838).
func TestPythonApplicationGateStillChecksTheModel838(t *testing.T) {
	application := callers838Application(t, "from .admission import admission\n", callers838Call)
	broken := strings.Replace(application, "    counts['value'] = 3\n    counts['value'] = 2\n", "    counts['value'] = 3\n    counts['value'] = 9\n", 1)
	// The driver states no expectations of its own, so the model alone
	// judges the states the application reached.
	driver := strings.NewReplacer(", expected_before=state, expected_after=expected", "", ", expected_before=state, expected_after=state", "",
		"expected_before=state, expected_after=expected", "").Replace(hold393Driver)
	if broken == application || strings.Contains(driver, "expected_") {
		t.Fatal("the fixture moved")
	}
	run := func(t *testing.T, tc toolchain.Toolchain, real bool) {
		gp359Verifier(t, real)
		f := callers838Fixture(t, callers838Admission, broken, driver)
		r := pa342Unchanged(t, f, tc)
		_, conformance := pa342Explored(t, r)
		checks := callers838Checks(t, r)
		if r.Passed || conformance.passed() || checks.Own == 0 || len(checks.Findings) != 0 {
			t.Fatalf("passed=%v conformance=%v checks=%+v\n%s", r.Passed, conformance.passed(), checks, conformance.Result.Message)
		}
	}
	t.Run("actual Python and verifier doubles", func(t *testing.T) {
		tc, _ := hold393GateFake(t)
		// The transport's double takes every trace. Here it answers as TLC
		// does for a count the model never reaches.
		docker, err := exec.LookPath("docker")
		if err != nil {
			t.Fatal(err)
		}
		filename := filepath.Join(filepath.Dir(docker), "docker.py")
		b, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(b), "    passed(3,3)", `    if module=='Invariant_Conformance' and re.search(r'count \|-> 9',body):
        msg(2114,'Deadlock reached.'); msg(2217,'1: <Initial predicate>\n/\\ count = 0\n/\\ invariant_source = 1\n/\\ invariant_target = 2'); sys.exit(11)
    passed(3,3)`, 1)
		if body == string(b) {
			t.Fatal("the transport fixture changed")
		}
		if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		run(t, tc, false)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("pinned Python TLC and Nagini", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			run(t, tc, true)
		})
	}
}

// ContractChecksReport838 shares the gate's fixture with the receipt's
// tests: the application calls admission asking for requested.
func ContractChecksReport838(t *testing.T, requested string) *Report {
	t.Helper()
	tc, _ := hold393GateFake(t)
	gp359Verifier(t, false)
	application := callers838Application(t, "from .admission import admission\n", strings.Replace(callers838Call, "'engine'", "'"+requested+"'", 1))
	return pa342Run(t, callers838Fixture(t, callers838Admission, application, hold393Driver), tc)
}
