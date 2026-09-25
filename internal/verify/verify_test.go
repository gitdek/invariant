package verify

import (
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
	b, e, err := parseSandbox(out)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Passed || len(b.Steps) != 2 || !e.OK || e.States != 288 || e.Depth != 11 {
		t.Errorf("build = %+v, exploration = %+v", b, e)
	}

	failing := "twophase.go:3: undefined: Successors\n@@invariant vet=1\nFAIL\n@@invariant test=1\nundefined: Successors\n@@invariant agree=1\n"
	b, e, err = parseSandbox(failing)
	if err != nil {
		t.Fatal(err)
	}
	if b.Passed || b.Steps[0].Passed || e.OK || !strings.Contains(e.Message, "Successors(State) []State") {
		t.Errorf("build = %+v, exploration = %+v", b, e)
	}

	if _, _, err := parseSandbox("docker: no space left on device"); err == nil {
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

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{"EarlyCommit": "early-commit", "TCConsistent": "tcconsistent", "Bug": "bug"} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%s) = %s; want %s", in, got, want)
		}
	}
}
