package verify

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSandbox(t *testing.T) {
	out := strings.Join([]string{
		"@@invariant vet=0",
		"ok  \texample/twophase\t0.21s",
		"@@invariant test=0",
		"=== RUN   TestInvariantAgreement",
		"invariant-agreement states=288 depth=11",
		"--- PASS: TestInvariantAgreement (0.01s)",
		"@@invariant agree=0",
	}, "\n")
	b, e, err := parseSandbox(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Passed || len(b.Steps) != 2 || !e.OK || e.States != 288 || e.Depth != 11 {
		t.Errorf("build = %+v, exploration = %+v", b, e)
	}

	failing := "twophase.go:3: undefined: Successors\n@@invariant vet=1\nFAIL\n@@invariant test=1\nundefined: Successors\n@@invariant agree=1\n"
	b, e, err = parseSandbox(failing, false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Passed || b.Steps[0].Passed || e.OK || !strings.Contains(e.Message, "Successors(State) []State") {
		t.Errorf("build = %+v, exploration = %+v", b, e)
	}
	// An explorer with Try is told what the harness needs instead.
	if _, e, _ = parseSandbox(strings.ReplaceAll(failing, "Successors", "Abstract"), true); e.OK || !strings.Contains(e.Message, "Abstract(State) map[string]any") {
		t.Errorf("exploration = %+v", e)
	}

	if _, _, err := parseSandbox("docker: no space left on device", false); err == nil {
		t.Error("a sandbox that never reached its markers should be an error")
	}
}

func TestAgree(t *testing.T) {
	d := Design{Passed: true, DistinctStates: 288, Depth: 11}
	if a := agree(d, Exploration{OK: true, States: 288, Depth: 11}); !a.Passed {
		t.Errorf("matching state spaces should agree: %+v", a)
	}
	if a := agree(d, Exploration{OK: true, States: 144, Depth: 9}); a.Passed || !strings.Contains(a.Message, "144 states") {
		t.Errorf("different state spaces must not agree: %+v", a)
	}
	if a := agree(Design{Passed: false}, Exploration{OK: true, States: 288, Depth: 11}); a.Passed {
		t.Error("without a finished TLC run there's nothing to agree with")
	}
}

func TestPythonFunctions(t *testing.T) {
	src := `# +nagini
class State:
    def __init__(self) -> None:
        Ensures(True)

    def size(self) -> int:
        return 3


def step(s: State) -> None:
    Requires(True)

    def helper() -> None:
        pass
`
	all, contracts := pythonFunctions(src)
	if !reflect.DeepEqual(all, []string{"State.__init__", "State.size", "step"}) || !reflect.DeepEqual(contracts, []string{"State.__init__", "step"}) {
		t.Errorf("functions = %v, contracts = %v", all, contracts)
	}
}

func TestNaginiErrors(t *testing.T) {
	out := strings.Join([]string{
		"Verification failed",
		"Errors:",
		"Postcondition of tm_commit might not hold. Assertion s.commit_msg might not hold. (core.py@78.12--78.38).",
		"Branch conditions: ",
		`  (not (field "commit_msg" does not exist)) at core.py@84.4--84.34`,
		`  (not (field "tm_done" does not exist)) at core.py@83.4--83.20`,
		"The precondition of s.rm[r] might not hold. (core.py@124.4--124.13).",
		"Verification took 21.24 seconds.",
	}, "\n")
	want := []string{
		"core.py:78:12: Postcondition of tm_commit might not hold. Assertion s.commit_msg might not hold.",
		"core.py:124:4: The precondition of s.rm[r] might not hold.",
	}
	if got := naginiErrors("core.py", out); !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %q", got)
	}
	if got := naginiErrors("core.py", "Traceback (most recent call last):\nImportError: nope"); len(got) != 1 || !strings.Contains(got[0], "ImportError") {
		t.Errorf("output without an error list should come back whole: %q", got)
	}
}

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{"EarlyCommit": "early-commit", "TCConsistent": "tcconsistent", "Bug": "bug"} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%s) = %s; want %s", in, got, want)
		}
	}
}
