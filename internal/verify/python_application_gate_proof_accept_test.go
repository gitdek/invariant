package verify

// #359, step 6 of #294's ratified plan, built by hand (D-0195): the gate
// proves a selected function in its real module.
//
// The fixture is a counter application. Its selected function, admit, is
// annotated in place in counter/core.py, beside an async neighbor Nagini
// can't read and a sibling import, and it reaches one trusted boundary: the
// standard library's keyword.iskeyword in local runs, and the installed
// packaging wheel's is_normalized_name in real ones. Conformance on admit
// shows the application runs the declaration Nagini proved. The application
// ships Invariant's runtime stand-in for nagini_contracts at its root.
//
// Default runs execute the actual staged runner and collector with local
// Python, #342's Docker and TLC double, and a Nagini double that verifies
// the function unless its input holds the planted violation. With
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 the same cases also run with real
// Docker, the pinned Python image, TLC, the pinned Nagini and the installed
// packaging wheel:
//
//	INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify \
//	  -run '^TestPythonApplicationGateProves' -count=1 -timeout 60m

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
)

const gp359Source = "counter/core.py"
const gp359Contracts = pa342Project + "/contracts.py"
const gp359Violation = "    return count - 1\n"

// gp359Core is the module that holds the selected function. boundary
// imports the operation admit reaches, and admits names it.
func gp359Core(boundary, admits string) string {
	return `from nagini_contracts.contracts import *
` + boundary + `
from .names import describe

STEP = 1


def admit(name: str, count: int) -> int:
    Requires(count >= 0)
    Ensures(Result() >= count)
    Ensures(Result() <= count + STEP)
    if ` + admits + `:
        return count + STEP
    return count


async def report(name: str) -> str:
    return describe(name)
`
}

const gp359Driver = `from invariant_observe import functional
from counter.core import admit
from counter import service


def drive():
    with functional(admit, service.start, argument='count', field='count') as life:
        life.step(service.inc)
        life.step(service.inc)


if __name__ == '__main__':
    drive()
`

const gp359ContractSource = `from nagini_contracts.contracts import *


def admissible(name: str) -> bool:
    Ensures(Implies(Result(), len(name) > 0))
    ...


def keyword(name: str) -> bool:
    Ensures(Implies(Result(), len(name) > 0))
    ...


def dumps(value: object) -> str:
    Ensures(len(Result()) > 0)
    ...
`

const gp359Tests = `import json
import unittest
from keyword import iskeyword


class KeywordTests(unittest.TestCase):
    def test_recognizes_keywords(self):
        self.assertTrue(iskeyword('if'))

    def test_rejects_names(self):
        self.assertFalse(iskeyword('counter'))
        self.assertFalse(iskeyword(''))


class NormalizedTests(unittest.TestCase):
    def test_accepts_normalized(self):
        from packaging.utils import is_normalized_name
        self.assertTrue(is_normalized_name('counter'))

    def test_rejects_unnormalized(self):
        from packaging.utils import is_normalized_name
        self.assertFalse(is_normalized_name('Not_Normal'))
        self.assertFalse(is_normalized_name(''))


class DumpsTests(unittest.TestCase):
    def test_dumps(self):
        self.assertEqual(json.dumps({'a': 1}), '{"a": 1}')
`

// gp359Boundary is the trusted boundary admit reaches, by mode.
type gp359Boundary struct {
	id       string
	contract string
	op       project.ComponentOperation
	tests    []string
	imports  string
	admits   string
}

func gp359Of(wheel bool) gp359Boundary {
	if wheel {
		return gp359Boundary{id: "packaging.is_normalized_name", contract: "admissible",
			op:      project.ComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.utils", Symbol: "is_normalized_name"},
			tests:   []string{"NormalizedTests.test_accepts_normalized", "NormalizedTests.test_rejects_unnormalized"},
			imports: "from packaging.utils import is_normalized_name", admits: "is_normalized_name(name)"}
	}
	return gp359Boundary{id: "keyword.iskeyword", contract: "keyword",
		op:      project.ComponentOperation{Kind: "standard_library", Module: "keyword", Symbol: "iskeyword"},
		tests:   []string{"KeywordTests.test_recognizes_keywords", "KeywordTests.test_rejects_names"},
		imports: "from keyword import iskeyword", admits: "not iskeyword(name)"}
}

func gp359Contract(id, contract string, op project.ComponentOperation, tests ...string) project.ComponentContract {
	c := project.ComponentContract{ID: id, Operation: op, Contract: pa342Ref(gp359Contracts, contract)}
	for _, test := range tests {
		ref := pa342Ref("tests/test_contracts.py", test)
		c.Tests = append(c.Tests, ref)
		c.Invocations = append(c.Invocations, project.ComponentInvocation{ID: strings.ReplaceAll(test, ".", "-"), Kind: "test", Check: ref,
			Command: "python", Args: []string{"-m", "unittest", "tests.test_contracts." + test}, WorkingDir: "."})
	}
	return c
}

// gp359New makes the counter application with admit selected for in-place
// proof, beside its test and conformance, the trusted contract on its
// boundary, and an unused declared contract on json.dumps.
func gp359New(t *testing.T, wheel bool) pa342Fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	p := &project.Project{Dir: filepath.Join(root, pa342Project), Manifest: project.Manifest{Name: "proved counter", Language: "python", Code: ".",
		Module: ".invariant/specs/Counter.tla", Conformance: "driver.py", Existing: []string{gp359Source}},
		Lock: project.Lock{Decision: "test-fixture-only", Bounds: map[string]string{"Limit": "2"}}}
	for _, s := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}, {Name: "Full", Kind: project.Witness}, {Name: "Overflow", Kind: project.Bug, Expect: "Safe"}} {
		h, err := tla.PinHash(pa342Model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		s.SHA256, s.Says = h, s.Name+" fixture obligation"
		p.Lock.Statements = append(p.Lock.Statements, s)
	}
	b := gp359Of(wheel)
	files := map[string]string{
		"counter/__init__.py": "", gp359Source: gp359Core(b.imports, b.admits),
		"counter/names.py":        "def describe(name):\n    return 'counter ' + name\n",
		"counter/service.py":      "from .core import admit\n\n\ndef start():\n    return 0\n\n\ndef inc(count):\n    return admit('counter', count)\n",
		"tests/__init__.py":       "",
		"tests/test_counter.py":   "import unittest\nfrom counter import service\n\n\nclass CounterTests(unittest.TestCase):\n    def test_inc(self):\n        self.assertEqual(service.inc(0), 1)\n",
		"tests/test_contracts.py": gp359Tests,
		pa342DriverPath:           gp359Driver, pa342ModelPath: pa342Model, gp359Contracts: gp359ContractSource,
		pa342Project + "/.invariant/specs/Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n",
	}
	// The application ships Invariant's runtime stand-in, so its module
	// imports its contracts wherever it runs.
	for _, name := range []string{"__init__.py", "contracts.py"} {
		body, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files["nagini_contracts/"+name] = string(body)
	}
	if wheel {
		files["requirements.txt"] = "-r deps/application.txt\n"
		files["deps/application.txt"] = "packaging>=24,<25\n-c constraints.txt\n"
		files["deps/constraints.txt"] = "packaging==24.2\n"
	}
	unit := pa342Ref("tests/test_counter.py", "CounterTests.test_inc")
	driver := pa342Ref(pa342DriverPath, "drive")
	target := pa342Ref(gp359Source, "admit")
	p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"counter"}, Checks: []project.ComponentCheck{{Target: target,
		Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}}, Tests: []project.ComponentRef{unit}, Conformance: []project.ComponentRef{driver},
		Invocations: []project.ComponentInvocation{
			{ID: "unit", Kind: "test", Check: unit, Command: "python", Args: []string{"-m", "unittest", "tests.test_counter.CounterTests.test_inc"}, WorkingDir: "."},
			{ID: "sample", Kind: "conformance", Check: driver, Command: "python", Args: []string{"driver.py"}, WorkingDir: pa342Project, Sampling: map[string]string{"runs": "2", "steps": "4", "seed": "1"},
				Inputs: []string{pa342ModelPath, pa342Project + "/.invariant/specs/Local.tla", pa342Project + "/.invariant/ratified.lock"}}}}},
		TrustedContracts: []project.ComponentContract{
			gp359Contract(b.id, b.contract, b.op, b.tests...),
			gp359Contract("json.dumps", "dumps", project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}, "DumpsTests.test_dumps"),
		}}
	for name, body := range files {
		pa342Write(t, root, name, body)
	}
	f := pa342Fixture{root, p}
	pa342Save(t, f)
	return f
}

// gp359Verifier stands in for the pinned Nagini in local runs, as the gate's
// session options: it verifies the input, unless it holds the planted
// violation, which it reports as Nagini does, at the input's line. Real runs
// keep the gate's own options. It returns how many times Nagini ran.
func gp359Verifier(t *testing.T, real bool) *int {
	t.Helper()
	calls := new(int)
	if real {
		return calls
	}
	saved := pythonApplicationGateOptions
	pythonApplicationGateOptions = PythonApplicationSessionOptions{
		Nagini: func(_ context.Context, c PythonApplicationNaginiCommand) (PythonApplicationProcess, error) {
			*calls++
			b, err := os.ReadFile(filepath.Join(c.Dir, c.Main))
			if err != nil {
				return PythonApplicationProcess{}, err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if line+"\n" == gp359Violation {
					return PythonApplicationProcess{ExitCode: 1, Stdout: fmt.Sprintf("Verification failed\nErrors:\nPostcondition of admit might not hold. Assertion (Result() >= count) might not hold. (%s@%d.4--%d.20).\nVerification took 3.20 seconds.\n", c.Main, i+1, i+1)}, nil
				}
			}
			return PythonApplicationProcess{Stdout: ppVerified}, nil
		},
		Distributions: func(context.Context, string) (map[string][]string, error) { return map[string][]string{}, nil },
	}
	t.Cleanup(func() { pythonApplicationGateOptions = saved })
	return calls
}

func gp359Proof(t *testing.T, r *Report) ComponentProof {
	t.Helper()
	for _, row := range r.Components.Proofs {
		if row.Target == pa342ID(gp359Source, "admit") {
			return row
		}
	}
	t.Fatalf("no proof row for admit: %+v", r.Components.Proofs)
	return ComponentProof{}
}

func gp359Standing(t *testing.T, r *Report, id string) ComponentContractEvidence {
	t.Helper()
	for _, c := range r.Components.Contracts {
		if c.Contract == id {
			return c
		}
	}
	t.Fatalf("no standing for %s: %+v", id, r.Components.Contracts)
	return ComponentContractEvidence{}
}

func gp359Dump(t *testing.T, r *Report) string {
	b, _ := json.MarshalIndent(r, "", " ")
	return string(b)
}

func TestPythonApplicationGateProvesInPlace(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		calls := gp359Verifier(t, real)
		f := gp359New(t, real)
		b := gp359Of(real)
		if err := Check(f.p.Dir); err != nil {
			t.Fatalf("a supported in-place proof must be admitted: %v", err)
		}
		before := pa342Tree(t, f.root)
		r := pa342Run(t, f, tc)
		if !r.Passed {
			t.Fatalf("the valid in-place proof failed the gate: %s", gp359Dump(t, r))
		}
		if !real && *calls != 1 {
			t.Fatalf("Nagini ran %d times, want once", *calls)
		}
		// The selected function is proved, conditional on its one assumed
		// contract, which is tested; the unused declaration stays one.
		row := gp359Proof(t, r)
		if row.Status != "proved" || len(row.Result.Assumed) != 1 || row.Result.Assumed[0].Contract != b.id || len(row.Result.Unaccounted) != 0 ||
			!reflect.DeepEqual(row.Result.Verified, []string{pa342ID(gp359Source, "admit")}) || row.Result.Tool == nil || row.Result.Tool.Pins["recipe"] != toolchain.NaginiRecipe() {
			t.Fatalf("admit's proof: %+v", row)
		}
		if s := gp359Standing(t, r, b.id); s.Support != "tested" || s.Use != "assumed" {
			t.Fatalf("the assumed boundary: %+v", s)
		}
		if s := gp359Standing(t, r, "json.dumps"); s.Support != "tested" || s.Use != "declared" || len(s.Relying) != 0 {
			t.Fatalf("an unused declaration must stay a declaration: %+v", s)
		}
		// Every other component, the async neighbor included, is unchecked
		// for proof.
		for _, p := range r.Components.Proofs {
			if p.Target != row.Target && p.Status != "unchecked" {
				t.Fatalf("an unselected component gained proof evidence: %+v", p)
			}
		}
		if !slices.ContainsFunc(r.Components.Proofs, func(p ComponentProof) bool { return p.Target == pa342ID(gp359Source, "report") }) {
			t.Fatalf("the neighbor isn't in the inventory: %+v", r.Components.Proofs)
		}
		// The input holds the function's original bytes and none of the
		// neighbor's, and the attempt records the recipe, not a local image.
		if len(r.Application.Proofs) != 1 {
			t.Fatalf("proof attempts: %+v", r.Application.Proofs)
		}
		attempt := r.Application.Proofs[0]
		main := ""
		for _, file := range attempt.Input.Files {
			if file.Path == PythonApplicationProofMain {
				main = file.Content
			}
		}
		if !strings.Contains(main, "def admit(name: str, count: int) -> int:") || strings.Contains(main, "async def report") || !slices.Contains(attempt.Input.Excluded, pa342ID(gp359Source, "report")) {
			t.Fatalf("the verifier input: %+v", attempt.Input)
		}
		if !attempt.Ran || attempt.Recipe != toolchain.NaginiRecipe() || r.Toolchain.NaginiRecipe != toolchain.NaginiRecipe() {
			t.Fatalf("the attempt's pins: %+v", attempt)
		}
		// Conformance and tests keep their own evidence.
		if len(r.Components.Conformance) != 1 || r.Components.Conformance[0].Status != "tested" {
			t.Fatalf("conformance on admit: %+v", r.Components.Conformance)
		}
		if problems := PythonApplicationProofProblems(r.Components); len(problems) != 0 {
			t.Fatalf("a qualified proof has problems: %v", problems)
		}
		// Nothing in the application changed, neighbors and stand-in included.
		if !reflect.DeepEqual(before, pa342Tree(t, f.root)) {
			t.Fatal("the gate changed the application")
		}
	})
}

// gp359Edit replaces old with new in a fixture file.
func gp359Edit(t *testing.T, f pa342Fixture, name, old, new string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s has no %q", name, old)
	}
	pa342Write(t, f.root, name, strings.Replace(string(b), old, new, 1))
}

func TestPythonApplicationGateProvesNothingItShouldnt(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		b := gp359Of(real)
		admit := pa342ID(gp359Source, "admit")
		t.Run("a function-body violation", func(t *testing.T) {
			gp359Verifier(t, real)
			f := gp359New(t, real)
			// Conformance never takes the branch, so only the proof sees it.
			gp359Edit(t, f, gp359Source, "    return count\n", gp359Violation)
			r := pa342Run(t, f, tc)
			row := gp359Proof(t, r)
			if r.Passed || row.Status != "unchecked" || row.Result == nil || row.Result.Passed || len(row.Result.Errors) == 0 || !strings.HasPrefix(row.Result.Errors[0], gp359Source+":") {
				t.Fatalf("a violation passed, or wasn't placed in the application: %+v", row)
			}
			if r.Components.Conformance[0].Status != "tested" || gp359Standing(t, r, b.id).Support != "tested" {
				t.Fatalf("a failed proof erased independent evidence: %+v", r.Components)
			}
			if p := PythonApplicationProofProblems(r.Components); len(p) != 1 || !strings.Contains(p[0], admit+" isn't proved") {
				t.Fatalf("problems: %v", p)
			}
		})
		t.Run("an omitted trusted contract", func(t *testing.T) {
			calls := gp359Verifier(t, real)
			f := gp359New(t, real)
			f.p.Manifest.Components.TrustedContracts = f.p.Manifest.Components.TrustedContracts[1:]
			pa342Save(t, f)
			r := pa342Run(t, f, tc)
			row := gp359Proof(t, r)
			if r.Passed || row.Status != "unchecked" || len(row.Result.Unaccounted) != 1 || row.Result.Unaccounted[0].Kind != MissingContract {
				t.Fatalf("an omitted contract passed: %+v", row)
			}
			if !real && *calls != 0 {
				t.Fatal("Nagini ran on an incomplete input")
			}
			if len(r.Application.Proofs) != 1 || r.Application.Proofs[0].Ran {
				t.Fatalf("the attempt: %+v", r.Application.Proofs)
			}
		})
		t.Run("a failing backing test", func(t *testing.T) {
			gp359Verifier(t, real)
			f := gp359New(t, real)
			if real {
				gp359Edit(t, f, "tests/test_contracts.py", "self.assertTrue(is_normalized_name('counter'))", "self.assertTrue(is_normalized_name('Not_Normal'))")
			} else {
				gp359Edit(t, f, "tests/test_contracts.py", "self.assertTrue(iskeyword('if'))", "self.assertTrue(iskeyword('counter'))")
			}
			r := pa342Run(t, f, tc)
			if r.Passed || gp359Standing(t, r, b.id).Support == "tested" {
				t.Fatalf("a failing backing test supported its contract: %s", gp359Dump(t, r))
			}
			p := PythonApplicationProofProblems(r.Components)
			if !slices.ContainsFunc(p, func(s string) bool {
				return strings.Contains(s, "assumes trusted contract "+b.id+", which isn't tested")
			}) {
				t.Fatalf("a failed required contract still supports the proof: %v", p)
			}
		})
	})
}

func TestPythonApplicationGateProofEvidenceFollowsItsInputs(t *testing.T) {
	tc, _ := pa342Fake(t)
	gp359Verifier(t, false)
	run := func(f pa342Fixture) *Report {
		t.Helper()
		r := pa342Run(t, f, tc)
		if !r.Passed {
			t.Fatalf("the case should still pass: %s", gp359Dump(t, r))
		}
		return r
	}
	f := gp359New(t, false)
	base := run(f)
	if again := run(f); again.Fingerprint != base.Fingerprint {
		t.Fatalf("identical runs differ: %s and %s", base.Fingerprint, again.Fingerprint)
	}
	input := func(r *Report) string { return r.Application.Proofs[0].Input.Fingerprint }
	for _, c := range []struct {
		name, file, old, new string
		input                bool
	}{
		{"the function's source", gp359Source, "    Ensures(Result() >= count)\n", "    # Never less than it was.\n    Ensures(Result() >= count)\n", true},
		{"its neighbor in the same module", gp359Source, "    return describe(name)\n", "    return describe(name).strip()\n", true},
		{"the assumed contract", gp359Contracts, "def keyword(name: str) -> bool:\n", "def keyword(name: str) -> bool:\n    \"\"\"Whether name is a keyword.\"\"\"\n", true},
		{"a backing test", "tests/test_contracts.py", "        self.assertFalse(iskeyword(''))\n", "        self.assertFalse(iskeyword(''))\n        self.assertFalse(iskeyword('Counter'))\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := gp359New(t, false)
			gp359Edit(t, f, c.file, c.old, c.new)
			r := run(f)
			if r.Fingerprint == base.Fingerprint {
				t.Fatalf("a changed %s left the fingerprint as it was", c.name)
			}
			if c.input != (input(r) != input(base)) {
				t.Fatalf("a changed %s: verifier input %s, was %s", c.name, input(r), input(base))
			}
		})
	}
}

func TestPythonApplicationGateRefusesWhatInPlaceProofNeeds(t *testing.T) {
	t.Run("an application without nagini_contracts", func(t *testing.T) {
		tc, _ := pa342Fake(t)
		gp359Verifier(t, false)
		f := gp359New(t, false)
		for _, name := range []string{"__init__.py", "contracts.py"} {
			if err := os.Remove(filepath.Join(f.root, "nagini_contracts", name)); err != nil {
				t.Fatal(err)
			}
		}
		r, err := Run(context.Background(), f.p.Dir, "", tc)
		if r != nil || err == nil || !strings.Contains(err.Error(), "imports nagini_contracts") {
			t.Fatalf("a module the application can't import was proved: %+v %v", r, err)
		}
	})
	t.Run("the direct Python check", func(t *testing.T) {
		gp359Verifier(t, false)
		f := gp359New(t, false)
		if b, _, err := (Python{}).Check(context.Background(), f.p); err == nil || b.Passed || !strings.Contains(err.Error(), "doesn't run") {
			t.Fatalf("the direct route ran a proof: %v", err)
		}
	})
	t.Run("a proof without conformance", func(t *testing.T) {
		f := gp359New(t, false)
		f.p.Manifest.Components.Checks[0].Conformance = nil
		f.p.Manifest.Components.Checks[0].Invocations = f.p.Manifest.Components.Checks[0].Invocations[:1]
		pa342Save(t, f)
		if err := Check(f.p.Dir); err == nil || !strings.Contains(err.Error(), "selects a proof and no conformance") {
			t.Fatalf("Check: %v", err)
		}
	})
}

// ProvedCounterReport runs the gate locally on the in-place proof fixture,
// changed by edit when it's given, for the receipt tests in verify_test,
// which can't import this package's fixtures.
func ProvedCounterReport(t *testing.T, edit func(root string)) *Report {
	t.Helper()
	tc, _ := pa342Fake(t)
	gp359Verifier(t, false)
	f := gp359New(t, false)
	if edit != nil {
		edit(f.root)
	}
	return pa342Run(t, f, tc)
}
