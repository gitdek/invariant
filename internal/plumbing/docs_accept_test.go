package plumbing

import (
	"strings"
	"testing"
)

// buildDocsPhrases is what a plumbing build's prompt tells its agent about
// SPEC.md and the README (#147, D-0122): they describe Invariant as it is,
// so when the plan names either one, the agent changes it as the plan's
// summary says, a SPEC bullet cites decisions already recorded or the plan
// itself, which is the change's decision record (#809), since the build
// can't write decisions/, and a decision the plan doesn't state is one
// whoever merges it records.
var buildDocsPhrases = []string{
	"SPEC.md and the README describe Invariant as it is",
	"When the plan names either one, change it as the plan's summary says",
	"The README is the front page, at most 400 lines",
	"detail goes in the page under docs/ it links",
	"A new or changed SPEC bullet cites what it rests on, as `decisions check` requires",
	"decisions already recorded, or this plan, by a link to its file, `[#147's ratified plan](.invariant/plans/issue-147.json)`",
	"which is this change's decision record (#809)",
	"You can't write decisions/",
	"the change needs a decision the plan doesn't state",
	"whoever merges it records the decision",
}

// A plan whose change makes SPEC.md wrong names it among its files. Its
// build's prompt says how to keep SPEC.md and the README right, still lists
// SPEC.md among the files the agent may write, and still gives the agent its
// test runs.
func TestBuildTellsTheAgentToKeepTheSpecAndReadmeRight(t *testing.T) {
	p := &Plan{
		Name:    "the plan prompt",
		Summary: "Change the plan prompt, and say so in SPEC.md.",
		Files:   []string{"SPEC.md", "internal/formalize/plan.go"},
		Tests:   []Test{{Name: "TestPlanPrompt", File: "internal/formalize/plan_accept_test.go", Says: "The prompt changed."}},
	}
	got := BuildPrompt(p, 147, 5)
	for _, want := range append([]string{"You have 5 runs", "- `SPEC.md`\n"}, buildDocsPhrases...) {
		if !strings.Contains(got, want) {
			t.Errorf("the build prompt lacks %q", want)
		}
	}
}
