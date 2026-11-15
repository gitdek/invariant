package verify

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
	"github.com/gitdek/invariant/internal/toolchain"
)

// The documentation is the delivered API. Place each complete copyable block
// immediately after <!-- plain-values: ID --> (whitespace is allowed), followed
// by its ordinary Markdown fence. IDs below are application-relative filenames
// or the names of the small rejected/working pairs. No production file or new
// helper is required: the same bytes readers copy are staged by these tests.
func pv923Block(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "python.md"))
	if err != nil {
		t.Fatal(err)
	}
	marker := "<!-- plain-values: " + id + " -->"
	if strings.Count(string(b), marker) != 1 {
		t.Fatalf("docs/python.md needs one executable plain-value example block %q", id)
	}
	tail := strings.TrimSpace(strings.SplitN(string(b), marker, 2)[1])
	if !strings.HasPrefix(tail, "```") {
		t.Fatalf("%s must label a fenced example", id)
	}
	_, body, ok := strings.Cut(tail, "\n")
	if !ok {
		t.Fatalf("%s has no source", id)
	}
	source, _, ok := strings.Cut(body, "\n```")
	if !ok || strings.TrimSpace(source) == "" {
		t.Fatalf("%s has no complete source fence", id)
	}
	return source + "\n"
}

// Example API: counter.admission.admission(has_site: bool, site: str,
// requested: str, busy: bool) -> int; counter.core.Request(headers, requested),
// reset(), submit(request), increment(...); module read-back field count is 0
// before the accepted request and 1 after. The handler directly imports
// admission from .admission. Its registry/object state and all effects remain
// ordinary Python. The model declares Spec, Safe, Full and Overflow (the last
// deliberately breaks Safe), with Limit=2 as its environment bound; its two
// states do not depend on that bound. Empty package initializers and the real
// shipped stand-in are the only sources supplied outside the documented blocks.
func pv923Fixture(t *testing.T) pa342Fixture {
	t.Helper()
	core := pv923Block(t, pa342Source)
	driver := pv923Block(t, pa342DriverPath)
	model := pv923Block(t, pa342ModelPath)
	admission := pv923Block(t, calls793Source)
	manifest := pv923Block(t, pa342Project+"/.invariant/invariant.json")
	f := refusal392Fixture(t, true, core, driver, model)
	// Remove the unrelated baseline example's tests and sources. The manifest
	// below is used verbatim, without adding a missing observe or proof selection.
	for _, name := range []string{"tests", "counter/service.py", "counter/step.py", "counter/step.txt", "counter/neighbor.py", "resources"} {
		if err := os.RemoveAll(filepath.Join(f.root, name)); err != nil {
			t.Fatal(err)
		}
	}
	pa342Write(t, f.root, calls793Source, admission)
	for name, body := range standIn908Files(t) {
		pa342Write(t, f.root, "nagini_contracts/"+name, string(body))
	}
	f.p.Manifest = project.Manifest{}
	if err := json.Unmarshal([]byte(manifest), &f.p.Manifest); err != nil {
		t.Fatalf("documented manifest: %v", err)
	}
	pa342Save(t, f) // Only this test's synthetic lock receives fixture pins.
	return f
}

func pv923Python(t *testing.T, root, script string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-B", "-c", "import sys\nsys.path.insert(0, sys.argv[1])\n"+script, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("documented Python failed: %v\n%s", err, out)
	}
}

func pv923Calls(t *testing.T, f pa342Fixture, r *Report) {
	t.Helper()
	x, c := pa342Explored(t, r)
	if !x.Success || !x.Complete || !c.passed() || !c.Result.Exhaustive || c.Result.Tried == nil || !c.Result.Tried.Passed {
		t.Fatalf("documented exploration must complete and conform: %s", gp359Dump(t, r))
	}
	seen := make(map[string]bool)
	for _, row := range x.Observations {
		if row.Kind != "transition" || row.Replay == nil || *row.Replay || row.Step != "Submit" {
			continue
		}
		if len(row.Calls) != 1 {
			t.Fatalf("every Submit entry must record its decision exactly once: %+v", row)
		}
		call := row.Calls[0]
		if call.Target != pa342ID(calls793Source, "admission") || call.Caller != pa342Source || call.SourceSHA256 != pythonApplicationHash([]byte(pv923Block(t, calls793Source))) || call.Raised != nil {
			t.Fatalf("decision evidence must name the staged declaration and application caller: %+v", call)
		}
		hasSite, h := call.Arguments["has_site"].(bool)
		site, s := call.Arguments["site"].(string)
		requested, q := call.Arguments["requested"].(string)
		busy, b := call.Arguments["busy"].(bool)
		if !h || !s || !q || !b || len(call.Arguments) != 4 || (!hasSite && site != "") {
			t.Fatalf("handler must pass normalized plain values: %+v", call)
		}
		want := 0
		switch {
		case hasSite && site != "statue":
			want = 403
		case requested != "engine":
			want = 400
		case busy:
			want = 409
		}
		if call.Result != json.Number(fmt.Sprint(want)) || row.Called == nil {
			t.Fatalf("decision returned the wrong precedence or result: %+v", row)
		}
		if want == 0 {
			if row.Outcome != "accepted" || !*row.Called {
				t.Fatalf("acceptance must reach the selected operation: %+v", row)
			}
		} else if row.Outcome != "refused" || *row.Called || row.Refusal == nil || !reflect.DeepEqual(row.Before, row.After) {
			t.Fatalf("refusal must escape before the selected operation with unchanged read-back: %+v", row)
		}
		seen[fmt.Sprintf("%t/%s/%s/%t/%d", hasSite, site, requested, busy, want)] = true
	}
	for _, want := range []string{
		"false//engine/false/0", "true/statue/engine/false/0", "true//engine/false/403",
		"true/elsewhere/engine/false/403", "false//other/false/400",
		"false//engine/true/409", "true/statue/engine/true/409", "true//engine/true/403",
	} {
		if !seen[want] {
			t.Errorf("documented exploration did not record %s; saw %v", want, seen)
		}
	}
}

func TestPythonApplicationGatePlainValues923(t *testing.T) {
	// Load before any optional tool path so absence is a completed assertion
	// failure on the base, never an environment skip or missing-helper error.
	pv923Block(t, calls793Source)
	run := func(t *testing.T, tc toolchain.Toolchain, real bool) {
		gp359Verifier(t, real)
		f := pv923Fixture(t)
		if err := Check(f.p.Dir); err != nil {
			t.Fatalf("documented proof selection must pass current admission: %v", err)
		}
		pv923Python(t, f.root, `from counter import core
from counter.admission import admission
for present, site in [(False, ''), (True, ''), (True, 'statue'), (True, 'elsewhere')]:
    for requested in ['engine', 'other']:
        for busy in [False, True]:
            want = 403 if present and site != 'statue' else 400 if requested != 'engine' else 409 if busy else 0
            assert admission(present, site, requested, busy) == want
for headers, requested, want in [({}, 'engine', 0), ({'Site': 'statue'}, 'engine', 0), ({'Site': ''}, 'engine', 403), ({'Site': 'elsewhere'}, 'other', 403), ({}, 'other', 400)]:
    core.reset()
    try:
        core.submit(core.Request(headers, requested))
    except core.Refused as error:
        assert want != 0 and error.status_code == want
    else:
        assert want == 0
core.reset()
core.submit(core.Request({}, 'engine'))
try:
    core.submit(core.Request({}, 'engine'))
except core.Refused as error:
    assert error.status_code == 409
else:
    raise AssertionError('busy handler accepted another request')
`)
		r := pa342Unchanged(t, f, tc)
		p := calls793Proof(t, r)
		if !r.Passed || p.Status != "proved" || p.Result == nil || !p.Result.Passed || len(r.Application.Proofs) != 1 || !r.Application.Proofs[0].Ran || r.Application.Proofs[0].Recipe != toolchain.NaginiRecipe() {
			t.Fatalf("documented decision proof did not qualify: %s", gp359Dump(t, r))
		}
		pv923Calls(t, f, r)
		for _, row := range r.Components.Conformance {
			if row.Target == p.Target {
				t.Fatal("example must qualify through recorded calls, not invented conformance on admission")
			}
		}
		for _, row := range r.Components.Proofs {
			if row.Target != p.Target && row.Status != "unchecked" {
				t.Fatalf("ordinary application component acquired a proof: %+v", row)
			}
		}
		if r.EverySize() || len(PythonApplicationProofProblems(r.Components, r.Application)) != 0 {
			t.Fatal("receipt's scope or recorded-call qualification is wrong")
		}
		wire, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Report
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		if calls793Proof(t, &decoded).Status != "proved" || len(PythonApplicationProofProblems(decoded.Components, decoded.Application)) != 0 {
			t.Fatal("receipt round trip lost the qualified proof")
		}
	}
	t.Run("local Python and verifier doubles", func(t *testing.T) {
		tc, _ := refusal392GateFake(t)
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

func TestPythonApplicationGatePlainValuesCoverage923(t *testing.T) {
	pv923Block(t, calls793Source)
	for _, mode := range []string{"named without calls", "accepted only"} {
		t.Run(mode, func(t *testing.T) {
			tc, _ := refusal392GateFake(t)
			verifier := gp359Verifier(t, false)
			f := pv923Fixture(t)
			core := pv923Block(t, pa342Source)
			const imported = "from .admission import admission"
			if !strings.Contains(core, imported) {
				t.Fatal("documented handler must directly import admission from .admission")
			}
			pa342Write(t, f.root, pa342Source, strings.Replace(core, imported, "from .unobserved import admission", 1))
			policy := strings.Replace(pv923Block(t, calls793Source), "def admission(", "def policy(", 1)
			wrapper := "\ndef admission(has_site, site, requested, busy):\n    result = policy(has_site, site, requested, busy)\n"
			if mode == "accepted only" {
				policy += "\nfrom .admission import admission as observed\n"
				wrapper += "    if result == 0:\n        return observed(has_site, site, requested, busy)\n"
			}
			pa342Write(t, f.root, "counter/unobserved.py", policy+wrapper+"    return result\n")
			f.p.Manifest.Existing = append(f.p.Manifest.Existing, "counter/unobserved.py")
			pa342Save(t, f)
			r := pa342Unchanged(t, f, tc)
			x, c := pa342Explored(t, r)
			p := calls793Proof(t, r)
			if !x.Success || !c.passed() || !c.Result.Exhaustive || c.Result.Tried == nil || !c.Result.Tried.Passed {
				t.Fatalf("independent operation conformance must still pass: %s", gp359Dump(t, r))
			}
			if r.Passed || p.Status != "unchecked" || p.Result == nil || p.Result.Passed || *verifier != 0 || len(r.Application.Proofs) != 1 || r.Application.Proofs[0].Ran || len(r.Application.Proofs[0].Skipped) == 0 {
				t.Fatalf("%s incorrectly qualified the decision or ran Nagini: %s", mode, gp359Dump(t, r))
			}
			accepted := 0
			for _, row := range x.Observations {
				for _, call := range row.Calls {
					if call.Target == p.Target {
						if mode == "named without calls" || row.Outcome != "accepted" {
							t.Fatalf("negative example has unintended decision calls: %+v", row)
						}
						accepted++
					}
				}
			}
			if mode == "accepted only" && accepted == 0 {
				t.Fatal("accepted-only case did not exercise the decision")
			}
		})
	}
}

// Each small pair is a complete Python module with an undecorated decision
// function. Working signatures are (has_site: bool, site: str) -> bool,
// (busy: bool) -> bool, and (active: int) -> bool respectively. The surrounding
// prose and the main handler show how the ordinary caller obtains these values.
func TestPythonApplicationProofPlainValueLimits923(t *testing.T) {
	cases := []struct{ id, reason string }{
		{"optional-string-rejected", "an Optional or a Union"},
		{"optional-arithmetic-rejected", "an Optional or a Union"},
		{"union-rejected", "an Optional or a Union"},
		{"subscript-rejected", "a subscript"},
		{"attribute-rejected", "an attribute read"},
		{"method-rejected", "a method call"},
		{"optional-working", ""}, {"lookup-working", ""}, {"setdefault-working", ""},
		// setdefault in a body is not a contract-admission restriction. Its
		// unsupported translation is checked against the real pinned Nagini.
		{"setdefault-rejected", ""},
	}
	root := t.TempDir()
	for name, body := range standIn908Files(t) {
		pa342Write(t, root, "nagini_contracts/"+name, string(body))
	}
	setdefaultLine := 0
	for _, c := range cases {
		source := pv923Block(t, c.id)
		if c.id == "setdefault-rejected" {
			// The statement form reports an unsupported builtin; assigning
			// its result instead crashes the pinned Nagini translator.
			for i, line := range strings.Split(source, "\n") {
				if strings.TrimSpace(line) == "d.setdefault(k, 0)" {
					if setdefaultLine != 0 {
						t.Fatal("setdefault-rejected must show exactly one d.setdefault(k, 0) statement")
					}
					setdefaultLine = i + 1
				}
			}
			if setdefaultLine == 0 {
				t.Fatal("setdefault-rejected must show d.setdefault(k, 0) as a statement, without assigning its result")
			}
		}
		d, err := analyzePythonProof([]byte(source), "decision")
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		if c.reason == "" {
			if len(d.Contracts) != 0 || len(d.Arithmetic) != 0 {
				t.Fatalf("%s was refused at admission: contracts=%+v arithmetic=%+v", c.id, d.Contracts, d.Arithmetic)
			}
		} else {
			found := false
			for _, problem := range d.Contracts {
				found = found || strings.Contains(problem.What, c.reason)
			}
			if !found {
				t.Fatalf("%s must be refused for %q, got %+v", c.id, c.reason, d.Contracts)
			}
		}
		pa342Write(t, root, strings.ReplaceAll(c.id, "-", "_")+".py", source)
	}
	pv923Python(t, root, `from optional_working import decision as optional
from lookup_working import decision as lookup
from setdefault_working import decision as available
from optional_string_rejected import decision as bad_string
from optional_arithmetic_rejected import decision as bad_arithmetic
assert optional(False, '') is True
assert optional(True, '') is False
assert optional(True, 'statue') is True
assert optional(True, 'elsewhere') is False
assert lookup(False) is True and lookup(True) is False
assert available(0) is True and available(1) is False
for bad in [bad_string, bad_arithmetic]:
    try:
        bad(None)
    except TypeError:
        pass
    else:
        raise AssertionError('Implies arguments did not evaluate eagerly')
`)
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_PROOF_REAL") == "1" {
		t.Run("pinned Nagini supports the replacements but not setdefault", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			image, err := toolchain.NaginiImage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"optional-working", "lookup-working", "setdefault-working", "setdefault-rejected"} {
				dir := t.TempDir()
				pa342Write(t, dir, "example.py", pv923Block(t, id))
				result, err := RunPythonApplicationNagini(ctx, PythonApplicationNaginiCommand{Image: image, Dir: dir, Main: "example.py"})
				if err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				output := result.Stdout + result.Stderr
				if id == "setdefault-rejected" {
					location := fmt.Sprintf("example.py@%d.", setdefaultLine)
					if result.ExitCode == 0 || !strings.Contains(output, "Unsupported builtin function") || !strings.Contains(output, location) {
						t.Fatalf("expected Nagini's Unsupported builtin function diagnostic at %s with a non-zero exit, got %+v", location, result)
					}
				} else if result.ExitCode != 0 || !strings.Contains(output, "Verification successful") {
					t.Fatalf("%s must verify with pinned Nagini: %+v", id, result)
				}
			}
		})
	}
}
