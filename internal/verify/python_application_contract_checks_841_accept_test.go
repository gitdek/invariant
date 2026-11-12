package verify

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// tests841 is the fixture's test file with lines added to its class and to
// its one test.
func tests841(class, test string) string {
	return fmt.Sprintf(`import unittest
from unittest import mock
from counter import core
from counter.admission import admission
class CounterTests(unittest.TestCase):
%s    def test_inc(self):
        core.reset()
        self.assertEqual(core.counts['active'], 0)
%s`, class, test)
}

// The gate checks a proved function's contract on the calls an
// application's own tests make, as it does on those exploration records
// (#841). The runner records each call of the function while the tests
// run, changing nothing the application sees, and each is checked whoever
// made it. Only a call the application's own code made counts as evidence.
func TestPythonApplicationGateChecksCallsTestsMake841(t *testing.T) {
	imports := "from .admission import admission\ndef allowed(requested, busy):\n    return admission(True, 'statue', requested, busy) == 0\n"
	application := callers838Application(t, imports, callers838Call)
	for _, c := range []struct {
		name, class, test string
		// tests is how many calls the test run recorded, and own how many
		// of them the application's own code made.
		tests, own int
		want       string
	}{
		{"through the application's code", "",
			"        self.assertTrue(core.allowed('engine', False))\n        self.assertFalse(core.allowed('boat', True))\n", 2, 2, ""},
		{"from a class fixture", "    @classmethod\n    def setUpClass(cls):\n        cls.allowed = core.allowed('engine', False)\n",
			"        self.assertTrue(self.allowed)\n", 1, 1, ""},
		{"a mock in the function's place", "",
			"        with mock.patch('counter.core.admission', return_value=0):\n            self.assertTrue(core.allowed('car', False))\n", 0, 0, ""},
		{"no test reaches it", "", "", 0, 0, ""},
		{"directly, against the contract", "",
			"        self.assertEqual(admission(True, 'statue', 'car', False), 0)\n", 1, 0,
			"a call from tests/test_counter.py, which isn't the application's own code, during tests.test_counter.CounterTests.test_inc didn't meet Requires(requested == 'engine' or requested == 'boat'), with (busy=False, has_site=True, requested=\"car\", site=\"statue\") returning 0"},
		{"through the application, against the contract", "",
			"        self.assertTrue(core.allowed('car', False))\n", 1, 1,
			"a call from counter/core.py during tests.test_counter.CounterTests.test_inc didn't meet Requires(requested == 'engine' or requested == 'boat')"},
		{"an argument the record can't hold", "",
			"        self.assertEqual(admission(True, ('statue',), 'engine', False), 0)\n", 1, 0,
			"during tests.test_counter.CounterTests.test_inc couldn't be checked against site: str: the record can't hold site, a tuple"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tc, _ := hold393GateFake(t)
			gp359Verifier(t, false)
			f := callers838Fixture(t, callers838Admission, application, hold393Driver)
			pa342Write(t, f.root, "tests/test_counter.py", tests841(c.class, c.test))
			if err := Check(f.p.Dir); err != nil {
				t.Fatalf("check: %v", err)
			}
			r := pa342Unchanged(t, f, tc)
			var ran *PythonApplicationExecutionResult
			for i, x := range r.Application.Executions {
				if x.Key.Kind == "test" {
					ran = &r.Application.Executions[i]
				}
			}
			if ran == nil || !ran.Success || len(ran.Tests) != 1 || ran.Tests[0].Outcome != "passed" {
				t.Fatalf("the application's test must run and pass as it does without the recorder: %+v", ran)
			}
			checks := callers838Checks(t, r)
			problems := strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n")
			if checks.Tests != c.tests {
				t.Fatalf("the test run recorded %d calls, want %d: %+v\n%s", checks.Tests, c.tests, ran, problems)
			}
			explored := checks.Calls - checks.Tests
			if explored < 2 || checks.Own != explored+c.own {
				t.Errorf("%d calls, %d of them while tests ran, %d by the application's own code; want %d of its own from tests", checks.Calls, checks.Tests, checks.Own, c.own)
			}
			if c.want == "" {
				if !r.Passed || !checks.Evidence() || problems != "" {
					t.Fatalf("calls that meet the contract didn't pass: %s", problems)
				}
				return
			}
			if r.Passed || calls793Proof(t, r).Status != Unqualified || !strings.Contains(problems, c.want) {
				t.Fatalf("passed=%v; the problems don't say\n  %s\nthey say\n  %s", r.Passed, c.want, problems)
			}
		})
	}
}

// The recorder runs in the pinned Python image as it does locally: the
// application's test passes there, its calls are recorded and checked, and
// the pinned Nagini and TLC take the same application.
func TestPythonApplicationGateChecksCallsTestsMakeReal841(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") != "1" {
		t.Skip("needs the pinned Python, TLC and Nagini")
	}
	tc, err := toolchain.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gp359Verifier(t, true)
	imports := "from .admission import admission\ndef allowed(requested, busy):\n    return admission(True, 'statue', requested, busy) == 0\n"
	f := callers838Fixture(t, callers838Admission, callers838Application(t, imports, callers838Call), hold393Driver)
	pa342Write(t, f.root, "tests/test_counter.py", tests841("    @classmethod\n    def setUpClass(cls):\n        cls.allowed = core.allowed('engine', False)\n",
		"        self.assertTrue(self.allowed)\n        self.assertFalse(core.allowed('boat', True))\n        self.assertEqual(admission(True, 'statue', 'engine', False), 0)\n"))
	r := pa342Unchanged(t, f, tc)
	checks := callers838Checks(t, r)
	// One call from the class fixture, one through the application and one
	// the test makes itself, which counts for nothing.
	if explored := checks.Calls - checks.Tests; !r.Passed || checks.Tests != 3 || checks.Own != explored+2 || !checks.Evidence() {
		t.Fatalf("passed=%v checks=%+v\n%s", r.Passed, checks, strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n"))
	}
}

// ContractChecksReport841 shares the gate's fixture with the receipt's
// tests: the application's test calls admission twice through its code.
func ContractChecksReport841(t *testing.T) *Report {
	t.Helper()
	tc, _ := hold393GateFake(t)
	gp359Verifier(t, false)
	imports := "from .admission import admission\ndef allowed(requested, busy):\n    return admission(True, 'statue', requested, busy) == 0\n"
	f := callers838Fixture(t, callers838Admission, callers838Application(t, imports, callers838Call), hold393Driver)
	pa342Write(t, f.root, "tests/test_counter.py", tests841("", "        self.assertTrue(core.allowed('engine', False))\n        self.assertFalse(core.allowed('boat', True))\n"))
	return pa342Run(t, f, tc)
}
