package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/verify"
)

func TestLivenessRows(t *testing.T) {
	r := &verify.Report{
		Project:   "answering",
		ModelOnly: true,
		Bounds:    map[string]string{"Commands": "{c1, c2}"},
		Design:    verify.Design{Passed: true, Outcome: "passed", DistinctStates: 9, StatesGenerated: 14, Depth: 5},
		Properties: []verify.Property{
			{Name: "EventuallyAnswered", Holds: true},
			{Name: "AnswersStay", Steps: 3, Loop: 2, Message: "TLC found a behavior that breaks it"},
		},
		Fairness: []verify.Fair{{Name: "WatcherIsFair", Action: "Watch", InNext: true}},
		Bugs:     []verify.Bug{{Name: "Drop", Label: "drop", Caught: true, Violated: "EventuallyAnswered", Steps: 5}},
	}
	md := Markdown(r)
	for _, want := range []string{
		"| Liveness · TLC | ❌ 1 of 2 properties hold | `EventuallyAnswered` holds, `AnswersStay` broken: loops back to state 2 after 3 steps, under `WatcherIsFair` |",
		"| Fairness | ✅ 1 of 1 on steps the model takes | `WatcherIsFair`: weak, on `Watch` |",
		"- Property `AnswersStay`: TLC found a behavior that breaks it",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt lacks %q:\n%s", want, md)
		}
	}

	// A project with no properties shows neither row, as before.
	r.Properties, r.Fairness = nil, nil
	if md := Markdown(r); strings.Contains(md, "Liveness") || strings.Contains(md, "Fairness") {
		t.Errorf("a receipt without properties shows liveness rows:\n%s", md)
	}
}

// A receipt names the coding agent that wrote the code, when its report
// names one, and nothing else about it (#179). The gate checks the code
// whoever wrote it, so two receipts that differ only in their agent share a
// fingerprint, and it's the one a report that names no agent has: a
// factory's receipt, which names the agent that built the code, matches
// CI's, which names the one its project's manifest records, if any.
func TestAReceiptNamesItsAgentOutsideTheFingerprint(t *testing.T) {
	r := verify.Report{
		Project:     "queue",
		Passed:      true,
		Bounds:      map[string]string{"Capacity": "1", "MaxPuts": "2"},
		Design:      verify.Design{Passed: true, Outcome: "passed", DistinctStates: 5},
		Build:       verify.Build{Passed: true},
		Assurance:   "tested against the model",
		Conformance: &conformance.Result{Passed: true, Exhaustive: true, Steps: 12, States: 5, ModelStates: 5},
	}
	codex, claude := r, r
	codex.Agent, claude.Agent = "codex", "claude-code"
	if a, b, none := verify.Fingerprint(codex), verify.Fingerprint(claude), verify.Fingerprint(r); a != b || a != none {
		t.Errorf("receipts that differ only in their agent have fingerprints %s, %s and, with none, %s", a, b, none)
	}
	for _, c := range []struct {
		report verify.Report
		want   string
	}{
		{codex, "Coding agent: `codex`. The fingerprint leaves it out.\n"},
		{claude, "Coding agent: `claude-code`. The fingerprint leaves it out.\n"},
	} {
		c.report.Fingerprint = verify.Fingerprint(c.report)
		if md := Markdown(&c.report); !strings.Contains(md, c.want) {
			t.Errorf("receipt lacks %q:\n%s", c.want, md)
		}
	}
	if md := Markdown(&r); strings.Contains(md, "Coding agent") {
		t.Errorf("a receipt whose report names no agent names one:\n%s", md)
	}
}

// A driver that records its attempts gets a row saying whether it tried
// every step; one that explores completely but records only runs says its
// steps weren't checked (D-0082).
func TestEveryStepTriedRow(t *testing.T) {
	r := &verify.Report{
		Project:   "queue",
		Bounds:    map[string]string{"Capacity": "1", "MaxPuts": "2"},
		Design:    verify.Design{Passed: true, Outcome: "passed", DistinctStates: 5},
		Build:     verify.Build{Passed: true},
		Assurance: "tested against the model",
		Conformance: &conformance.Result{Passed: true, Exhaustive: true, Steps: 12, States: 5, ModelStates: 5,
			Tried: &conformance.Tried{Passed: true, Attempts: 12, States: 5}},
	}
	for _, want := range []string{
		"| Every step tried | ✅ in every state reached, but where only the environment's bounds rule a step out | 12 attempts in 5 states, refusals included |",
		"| Code · conformance | ✅ tested against the model: every reachable state, no step outside it | 12 steps recorded, 5 of 5 model states visited |",
	} {
		if md := Markdown(r); !strings.Contains(md, want) {
			t.Errorf("receipt lacks %q:\n%s", want, md)
		}
	}
	r.Conformance.Tried = &conformance.Tried{Attempts: 11, States: 5, Untried: `{<<"Take">>}`, In: "q = <<>> /\\ puts = 0", Message: "the driver never tried a step that more than a bound rules out"}
	md := Markdown(r)
	for _, want := range []string{
		"| Every step tried | ❌ a step never tried | `{<<\"Take\">>}` in `q = <<>> /\\ puts = 0` |",
		"- Every step tried: the driver never tried a step that more than a bound rules out. `{<<\"Take\">>}` in `q = <<>> /\\ puts = 0`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt lacks %q:\n%s", want, md)
		}
	}
	r.Conformance.Tried = nil
	if md := Markdown(r); !strings.Contains(md, "| Every step tried | ➖ not checked | the driver records its runs, not its attempts (D-0082) |") {
		t.Errorf("a driver that records runs doesn't say its steps weren't checked:\n%s", md)
	}

	// A Go explorer with only Successors says its steps weren't checked. One
	// with Try has its attempts checked, beside its agreement (D-0090).
	r.Conformance, r.Agreement = nil, &verify.Agreement{Passed: true, States: 5, Depth: 3, WantStates: 5, WantDepth: 3}
	if md := Markdown(r); !strings.Contains(md, "| Every step tried | ➖ not checked | the explorer reports the states it reaches, not its attempts (D-0082) |") {
		t.Errorf("a Go explorer without Try doesn't say its steps weren't checked:\n%s", md)
	}
	r.Conformance = &conformance.Result{Passed: true, Exhaustive: true, Steps: 12, States: 5, ModelStates: 5,
		Tried: &conformance.Tried{Passed: true, Attempts: 12, States: 5}}
	md = Markdown(r)
	if strings.Contains(md, "not checked") || !strings.Contains(md, "| Every step tried | ✅") || !strings.Contains(md, "| Agreement | ✅") {
		t.Errorf("a Go explorer with Try:\n%s", md)
	}
}
