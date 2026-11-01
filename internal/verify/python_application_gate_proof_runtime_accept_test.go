package verify

// #359's second review: an in-place proof takes Invariant's runtime stand-in
// for nagini_contracts and the builtins as they ship, and the module runs its
// contract where it stands, evaluating every argument eagerly, whatever Nagini
// reads lazily. The gate refuses an application that changes either before
// anything runs, and a contract Python could fail evaluating at admission, and
// proves one that keeps to what can't fail.
//
// Every case runs locally, and on the real gate too with
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1, by the command in this package's
// in-place proof tests' header.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// gp359Refused runs the gate on f and wants it refused, with want in the
// error, before it returns a report.
func gp359Refused(t *testing.T, f pa342Fixture, tc toolchain.Toolchain, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	r, err := Run(ctx, f.p.Dir, "", tc)
	if r != nil || err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want a refusal saying %q, got report=%v err=%v", want, r != nil, err)
	}
}

func TestPythonApplicationGateRefusesChangesToTheStandIn(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		for _, c := range []struct{ name, init, want string }{
			// Pa: the package adds a min to the byte-identical stand-in, which
			// the module's star import then binds over the builtin Nagini
			// reads, so admit('counter', 500) would return -1.
			{"a package that adds to the stand-in",
				"from nagini_contracts import contracts\n\n\ndef _floor(a, b):\n    return -1 if a > 100 else (a if a < b else b)\n\n\n" +
					"contracts.min = _floor\ncontracts.__all__.append('min')\n",
				"counter/__init__.py:8 writes nagini_contracts.contracts.min, which changes Invariant's runtime stand-in for nagini_contracts"},
			// Pb: the package puts another module in the stand-in's place.
			{"a package that replaces the stand-in",
				"import sys\nimport types\n\nfake = types.ModuleType('nagini_contracts.contracts')\nsys.modules['nagini_contracts.contracts'] = fake\n",
				"counter/__init__.py:5 looks nagini_contracts.contracts up in sys.modules"},
			// The builtins Nagini models are taken as they ship too.
			{"a package that rebinds a builtin",
				"import builtins\n\n\ndef _floor(a, b):\n    return -1\n\n\nbuiltins.min = _floor\n",
				"counter/__init__.py:8 writes builtins.min, which changes what every module's builtins bind"},
		} {
			t.Run(c.name, func(t *testing.T) {
				calls := gp359Verifier(t, real)
				f := gp359New(t, real)
				gp359Edit(t, f, gp359Source, "        return count + STEP\n", "        return min(count + STEP, count + 1)\n")
				pa342Write(t, f.root, "counter/__init__.py", c.init)
				if err := Check(f.p.Dir); err != nil {
					t.Fatalf("what the function's own source shows is supported: %v", err)
				}
				gp359Refused(t, f, tc, c.want)
				if *calls != 0 {
					t.Fatal("Nagini ran on an application that changes what its proof takes as it ships")
				}
			})
		}
	})
}

func TestPythonApplicationGateRefusesContractsPythonCantRun(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		postcondition := "    Ensures(Result() <= count + STEP)\n"
		for _, c := range []struct {
			name  string
			edits [][2]string
			want  string
		}{
			// Pc: Nagini reads the consequent only where name isn't empty, and
			// Python divides by len(name) on entry, so admit('', 0) raises.
			{"an Implies consequent that divides", [][2]string{{postcondition,
				postcondition + "    Ensures(Implies(len(name) > 0, Result() - count <= len(name) // len(name)))\n"}},
				"line 12 Ensures holds a floor division, which Python evaluates when the statement runs, even where Nagini reads it lazily"},
			// Nagini reads Implies as implication, and the stand-in's is
			// always true, so admit('counter', 500) would return 501.
			{"Implies as a value", [][2]string{
				{postcondition, postcondition + "    Ensures(Implies(count > 100, Result() == count))\n"},
				{"    if ", "    if Implies(count > 100, False) and "}},
				"line 13 reads Nagini's Implies outside a contract statement, where Invariant's runtime stand-in for it gives back nothing Nagini's does"},
			// Nagini checks seen is bound only where the antecedent holds;
			// Python reads it on every call, so admit('counter', 0) raises.
			{"a local an Implies reads before every path binds it", [][2]string{{"    if ",
				"    if count > 0:\n        seen = count\n    Assert(Implies(count > 0, seen == count))\n    if "}},
				"line 14 Assert reads seen, which the function may not have bound when Python evaluates the contract"},
			// Result() stands for nothing a string could hold, and testing it
			// against one raises.
			{"Result() tested for membership in a string", [][2]string{{postcondition,
				postcondition + "    Ensures(Implies(len(name) > 0, Result() not in name))\n"}},
				"line 12 Ensures tests Result() for membership in something other than a tuple it lists"},
		} {
			t.Run(c.name, func(t *testing.T) {
				calls := gp359Verifier(t, real)
				f := gp359New(t, real)
				for _, e := range c.edits {
					gp359Edit(t, f, gp359Source, e[0], e[1])
				}
				if err := Check(f.p.Dir); err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("Check: want %q, got %v", c.want, err)
				}
				gp359Refused(t, f, tc, c.want)
				if *calls != 0 {
					t.Fatal("Nagini ran on a contract the application couldn't run as Nagini reads it")
				}
			})
		}
	})
}

// gp359Kept is the counter's module with a contract that holds only what
// Python can evaluate where the module runs without failing: comparisons, and,
// or and not, a conditional expression, +, - and *, Implies, Old and Result(),
// len, abs and isinstance, a loop invariant over locals bound ahead of it, and
// an Assert after the loop.
func gp359Kept(boundary, admits string) string {
	return `from nagini_contracts.contracts import *
` + boundary + `
from .names import describe

STEP = 1


def admit(name: str, count: int) -> int:
    Requires(count >= 0)
    Requires(isinstance(name, str) and abs(count) == count)
    Ensures(Result() >= count)
    Ensures(Result() <= count + STEP)
    Ensures(Implies(Result() > count, Result() - count == 1 * STEP and len(name) > 0))
    Ensures(Result() == count or Result() == count + STEP)
    Ensures(Result() >= Old(count) if len(name) > 0 else not Result() < count)
    total = count
    i = 0
    while i < STEP:
        Invariant(0 <= i and i <= STEP and total == count + i)
        total = total + 1
        i = i + 1
    Assert(Implies(i > 0, total == count + STEP))
    if ` + admits + `:
        return total
    return count


async def report(name: str) -> str:
    return describe(name)
`
}

func TestPythonApplicationGateProvesContractsPythonRunsAsNaginiReadsThem(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		calls := gp359Verifier(t, real)
		f := gp359New(t, real)
		b := gp359Of(real)
		pa342Write(t, f.root, gp359Source, gp359Kept(b.imports, b.admits))
		if err := Check(f.p.Dir); err != nil {
			t.Fatalf("a contract Python runs as Nagini reads it must be admitted: %v", err)
		}
		r := pa342Run(t, f, tc)
		if !r.Passed {
			t.Fatalf("the proof failed the gate: %s", gp359Dump(t, r))
		}
		if !real && *calls != 1 {
			t.Fatalf("Nagini ran %d times, want once", *calls)
		}
		if row := gp359Proof(t, r); row.Status != "proved" || len(row.Result.Unaccounted) != 0 {
			t.Fatalf("admit's proof: %+v", row)
		}
		if problems := PythonApplicationProofProblems(r.Components); len(problems) != 0 {
			t.Fatalf("a qualified proof has problems: %v", problems)
		}
	})
}
